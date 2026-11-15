// Command gpu-ctl is the provider's local control for a node agent started
// with `node-agent -serve`: list approved images, run one, drain, resume and
// reclaim.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/allowlist"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/control"
)

const usage = `Usage: gpu-ctl [-socket PATH] <command>

Commands:
  status                 show the node's state, active job and last result
  images                 list the images this node is allowed to run
  run [-for 30m] [IMAGE] run an approved image; IMAGE is its number from
                         "images", its digest or its repository. Without
                         IMAGE, gpu-ctl lists the images and asks.
  drain                  stop accepting new jobs; the current job keeps running
  resume                 accept new jobs again after a drain
  reclaim                stop the current job immediately

The node agent must be running: node-agent -serve -allowlist <file>
`

const (
	commandTimeout = 15 * time.Second
	reclaimTimeout = 45 * time.Second
	// runTimeout covers pulling the image before the job starts.
	runTimeout = 7 * time.Minute
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "gpu-ctl:", err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("gpu-ctl", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(stderr, usage) }
	socket := flags.String("socket", control.DefaultSocketPath(), "node agent control socket")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() == 0 {
		flags.Usage()
		return errors.New("missing command")
	}
	client := cli{socket: *socket, stdin: stdin, stdout: stdout, stderr: stderr}
	command, rest := flags.Arg(0), flags.Args()[1:]

	switch command {
	case "run":
		return client.run(ctx, rest)
	case "status", "images", "drain", "resume", "reclaim":
		if len(rest) != 0 {
			return fmt.Errorf("%s takes no arguments", command)
		}
	default:
		flags.Usage()
		return fmt.Errorf("unknown command %q", command)
	}

	timeout := commandTimeout
	if command == "reclaim" {
		timeout = reclaimTimeout
	}
	response, err := client.send(ctx, control.Request{Command: command}, timeout)
	if err != nil {
		return err
	}
	switch command {
	case "images":
		printImages(stdout, response.Images)
		return nil
	case "drain":
		fmt.Fprintln(stdout, "Draining: the node will not accept new jobs. Use \"gpu-ctl resume\" to accept them again.")
	case "resume":
		fmt.Fprintln(stdout, "Resumed: the node accepts new jobs.")
	case "reclaim":
		fmt.Fprintln(stdout, "Reclaimed: the job was stopped and its container removed.")
	}
	printStatus(stdout, response)
	return nil
}

type cli struct {
	socket string
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func (client cli) send(ctx context.Context, request control.Request, timeout time.Duration) (control.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return control.Send(ctx, client.socket, request)
}

func (client cli) run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(client.stderr)
	flags.Usage = func() { fmt.Fprint(client.stderr, usage) }
	duration := flags.Duration("for", 30*time.Minute, "how long the job may run, at most "+control.MaxRunDuration.String())
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return errors.New("run takes at most one image")
	}
	if *duration < time.Second || *duration > control.MaxRunDuration || *duration%time.Second != 0 {
		return fmt.Errorf("-for must be a whole number of seconds between 1s and %s", control.MaxRunDuration)
	}

	response, err := client.send(ctx, control.Request{Command: control.CommandImages}, commandTimeout)
	if err != nil {
		return err
	}
	images := response.Images
	if len(images) == 0 {
		return errors.New("the allowlist has no approved images")
	}

	choice := flags.Arg(0)
	if choice == "" {
		printImages(client.stdout, images)
		fmt.Fprintf(client.stdout, "Choose an image to run [1-%d]: ", len(images))
		line, err := bufio.NewReader(client.stdin).ReadString('\n')
		if err != nil && line == "" {
			return errors.New("no image chosen")
		}
		choice = strings.TrimSpace(line)
	}
	image, err := pickImage(images, choice)
	if err != nil {
		return err
	}

	fmt.Fprintf(client.stdout, "Starting %s (pulling the image may take a few minutes)...\n", image.Image)
	response, err = client.send(ctx, control.Request{
		Command: control.CommandRun,
		Digest:  image.Digest,
		Seconds: int(duration.Seconds()),
	}, runTimeout)
	if err != nil {
		return err
	}
	fmt.Fprintf(client.stdout, "Started lease %s. Check on it with \"gpu-ctl status\".\n", response.LeaseID)
	printStatus(client.stdout, response)
	return nil
}

// pickImage finds an image by its 1-based number, exact digest, or repository
// when only one approved image uses it.
func pickImage(images []allowlist.Entry, choice string) (allowlist.Entry, error) {
	if choice == "" {
		return allowlist.Entry{}, errors.New("no image chosen")
	}
	if number, err := strconv.Atoi(choice); err == nil {
		if number < 1 || number > len(images) {
			return allowlist.Entry{}, fmt.Errorf("choose a number from 1 to %d", len(images))
		}
		return images[number-1], nil
	}
	var matches []allowlist.Entry
	for _, image := range images {
		if image.Digest == choice {
			return image, nil
		}
		if image.Image == choice {
			matches = append(matches, image)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return allowlist.Entry{}, fmt.Errorf("%q is not an approved image; see \"gpu-ctl images\"", choice)
	default:
		return allowlist.Entry{}, fmt.Errorf("%s has %d approved digests; choose by number or digest", choice, len(matches))
	}
}

func printImages(out io.Writer, images []allowlist.Entry) {
	if len(images) == 0 {
		fmt.Fprintln(out, "No approved images.")
		return
	}
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "#\tREPOSITORY\tDIGEST")
	for index, image := range images {
		fmt.Fprintf(table, "%d\t%s\t%s\n", index+1, image.Image, image.Digest)
	}
	table.Flush()
}

func printStatus(out io.Writer, response control.Response) {
	fmt.Fprintf(out, "State:       %s\n", response.State)
	if active := response.Lease; active != nil {
		fmt.Fprintf(out, "Job:         %s\n", active.ID)
		fmt.Fprintf(out, "  Image:     %s\n", active.ImageDigest)
		fmt.Fprintf(out, "  Expires:   %s (in %s)\n",
			active.ExpiresAt.Local().Format("2006-01-02 15:04:05"),
			time.Until(active.ExpiresAt).Round(time.Second))
	} else {
		fmt.Fprintln(out, "Job:         none")
	}
	if last := response.LastResult; last != nil {
		outcome := string(last.Outcome)
		if last.ExitCode != nil {
			outcome += fmt.Sprintf(" (exit %d)", *last.ExitCode)
		}
		fmt.Fprintf(out, "Last result: %s %s\n", last.LeaseID, outcome)
	}
}
