package main

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/capability"
)

// Config holds local execution settings and informational machine specs.
type Config struct {
	Allowlist  string
	OfferPath  string
	ShowOutput bool
	GPUModel   string
	VRAMMB     int
	Driver     string
	Runtimes   []string
}

// promptConfig shows current settings, lets the user edit by number, then start.
func promptConfig(in *bufio.Reader, cfg Config) (Config, error) {
	fmt.Println("node-agent — local workload runner")
	fmt.Println("Pick a number to change a setting, s to start, q to quit.")
	for {
		printSettings(cfg)
		fmt.Print("> ")
		choice, err := readLine(in)
		if err != nil {
			return cfg, err
		}
		switch strings.ToLower(choice) {
		case "s", "start":
			if cfg.Allowlist == "" || cfg.OfferPath == "" {
				fmt.Println("Set both the allowlist and offer JSON file before starting.")
				continue
			}
			return cfg, nil
		case "q", "quit", "exit":
			return cfg, fmt.Errorf("quit")
		case "1":
			cfg.Allowlist, err = editString(in, "Allowlist JSON file", cfg.Allowlist)
		case "2":
			cfg.OfferPath, err = editString(in, "Offer JSON file", cfg.OfferPath)
		case "3":
			cfg.ShowOutput, err = editBool(in, "Show container output", cfg.ShowOutput)
		case "4":
			err = machineSpecsMenu(in, &cfg)
		default:
			fmt.Println("unknown choice; use 1-4, s, or q")
			continue
		}
		if err != nil {
			return cfg, err
		}
	}
}

func printSettings(cfg Config) {
	fmt.Println()
	fmt.Printf("  1) Allowlist JSON file  %s\n", disp(cfg.Allowlist))
	fmt.Printf("  2) Offer JSON file      %s\n", disp(cfg.OfferPath))
	fmt.Printf("  3) Show output          %v\n", cfg.ShowOutput)
	fmt.Printf("  4) Machine specs        %s\n", specsSummary(cfg))
	fmt.Println("     Specs are informational; they do not change container permissions.")
	fmt.Println("  s) Start")
	fmt.Println("  q) Quit")
}

func specsSummary(cfg Config) string {
	if cfg.GPUModel == "" && cfg.VRAMMB == 0 && cfg.Driver == "" && len(cfg.Runtimes) == 0 {
		return "(unset)"
	}
	parts := []string{}
	if cfg.GPUModel != "" {
		parts = append(parts, cfg.GPUModel)
	}
	if cfg.VRAMMB > 0 {
		parts = append(parts, fmt.Sprintf("%d MiB", cfg.VRAMMB))
	}
	if cfg.Driver != "" {
		parts = append(parts, "driver "+cfg.Driver)
	}
	if len(cfg.Runtimes) > 0 {
		parts = append(parts, strings.Join(cfg.Runtimes, "/"))
	}
	return strings.Join(parts, ", ")
}

func machineSpecsMenu(in *bufio.Reader, cfg *Config) error {
	for {
		fmt.Println()
		fmt.Println("Machine specs")
		fmt.Printf("  GPU model       %s\n", disp(cfg.GPUModel))
		fmt.Printf("  VRAM MiB        %d\n", cfg.VRAMMB)
		fmt.Printf("  Driver version  %s\n", disp(cfg.Driver))
		fmt.Printf("  Runtimes        %s\n", disp(strings.Join(cfg.Runtimes, ", ")))
		fmt.Println()
		fmt.Println("  1) Discover with nvidia-smi")
		fmt.Println("  2) Configure manually")
		fmt.Println("  b) Back")
		fmt.Print("> ")
		choice, err := readLine(in)
		if err != nil {
			return err
		}
		switch strings.ToLower(choice) {
		case "b", "back", "":
			return nil
		case "1":
			if err := discoverWithNVIDIASMI(cfg); err != nil {
				fmt.Printf("discovery failed: %v\n", err)
			}
		case "2":
			if err := manualSpecsMenu(in, cfg); err != nil {
				return err
			}
		default:
			fmt.Println("unknown choice; use 1, 2, or b")
		}
	}
}

func discoverWithNVIDIASMI(cfg *Config) error {
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
	rep, err := capability.NewGPUSMICollector(runner, 0).Capabilities(context.Background())
	if err != nil {
		return err
	}
	if len(rep.GPUs) == 0 {
		return fmt.Errorf("nvidia-smi reported no GPUs")
	}
	// Show the first GPU in the summary.
	gpu := rep.GPUs[0]
	cfg.GPUModel = gpu.Name
	cfg.VRAMMB = gpu.VRAMTotalMB
	cfg.Driver = rep.Driver.Version
	fmt.Printf("discovered %s (%d MiB), driver %s", cfg.GPUModel, cfg.VRAMMB, cfg.Driver)
	if len(rep.GPUs) > 1 {
		fmt.Printf(" — using GPU 0 of %d", len(rep.GPUs))
	}
	fmt.Println()
	fmt.Println("Runtimes are not reported by nvidia-smi; set them under Configure manually if needed.")
	return nil
}

func manualSpecsMenu(in *bufio.Reader, cfg *Config) error {
	for {
		fmt.Println()
		fmt.Println("Manual machine specs")
		fmt.Printf("  1) GPU model       %s\n", disp(cfg.GPUModel))
		fmt.Printf("  2) VRAM MiB        %d\n", cfg.VRAMMB)
		fmt.Printf("  3) Driver version  %s\n", disp(cfg.Driver))
		fmt.Printf("  4) Runtimes        %s\n", disp(strings.Join(cfg.Runtimes, ", ")))
		fmt.Println("  b) Back")
		fmt.Print("> ")
		choice, err := readLine(in)
		if err != nil {
			return err
		}
		switch strings.ToLower(choice) {
		case "b", "back", "":
			return nil
		case "1":
			cfg.GPUModel, err = editString(in, "GPU model", cfg.GPUModel)
		case "2":
			cfg.VRAMMB, err = editInt(in, "VRAM MiB", cfg.VRAMMB)
		case "3":
			cfg.Driver, err = editString(in, "Driver version", cfg.Driver)
		case "4":
			var s string
			s, err = editString(in, "Runtimes (comma-separated)", strings.Join(cfg.Runtimes, ","))
			if err == nil {
				cfg.Runtimes = splitCSV(s)
			}
		default:
			fmt.Println("unknown choice; use 1-4 or b")
			continue
		}
		if err != nil {
			return err
		}
	}
}

func disp(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

func editString(in *bufio.Reader, label, cur string) (string, error) {
	fmt.Printf("%s [%s]: ", label, disp(cur))
	line, err := readLine(in)
	if err != nil {
		return cur, err
	}
	if line == "" {
		return cur, nil
	}
	return line, nil
}

func editInt(in *bufio.Reader, label string, cur int) (int, error) {
	fmt.Printf("%s [%d]: ", label, cur)
	line, err := readLine(in)
	if err != nil {
		return cur, err
	}
	if line == "" {
		return cur, nil
	}
	n, err := strconv.Atoi(line)
	if err != nil {
		fmt.Printf("invalid int %q, keeping %d\n", line, cur)
		return cur, nil
	}
	return n, nil
}

func editBool(in *bufio.Reader, label string, cur bool) (bool, error) {
	fmt.Printf("%s [%v] (y/n): ", label, cur)
	line, err := readLine(in)
	if err != nil {
		return cur, err
	}
	if line == "" {
		return cur, nil
	}
	switch strings.ToLower(line) {
	case "y", "yes", "true", "1":
		return true, nil
	case "n", "no", "false", "0":
		return false, nil
	default:
		fmt.Printf("invalid bool %q, keeping %v\n", line, cur)
		return cur, nil
	}
}

func readLine(in *bufio.Reader) (string, error) {
	line, err := in.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
