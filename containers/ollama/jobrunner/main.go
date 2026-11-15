// Command ollama-job runs one inference job against the approved model and
// exits. See containers/ollama/JOB_CONTRACT.md for the input, output and exit
// codes.
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	requestPath := flag.String("request", "/job/input/request.json", "request file")
	outputDir := flag.String("output", "/job/output", "directory that receives response.json")
	verifyPath := flag.String("verify", "/usr/local/bin/verify-approved-model", "model verification command")
	ollamaPath := flag.String("ollama", "/bin/ollama", "ollama executable")
	host := flag.String("host", "127.0.0.1:11434", "loopback address for the internal Ollama server")
	startTimeout := flag.Duration("start-timeout", 60*time.Second, "time allowed for Ollama to become ready")
	jobTimeout := flag.Duration("timeout", 10*time.Minute, "time allowed for generation")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	r := &runner{
		config: config{
			// The entrypoint sets this from the approved-model.env baked into
			// the image, overriding any value passed at runtime.
			model:        os.Getenv("APPROVED_MODEL"),
			requestPath:  *requestPath,
			outputDir:    *outputDir,
			startTimeout: *startTimeout,
			jobTimeout:   *jobTimeout,
		},
		verify: verifyCommand(*verifyPath),
		start:  func() (server, error) { return startServer(*ollamaPath, *host) },
		client: newOllamaClient("http://" + *host),
		stderr: os.Stderr,
	}
	code := r.run(ctx)
	stop()
	os.Exit(code)
}
