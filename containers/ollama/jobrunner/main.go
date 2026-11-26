// Command ollama-job runs one inference job against the approved model and
// exits. See containers/ollama/JOB_CONTRACT.md for the input, output and exit
// codes.
//
// `ollama-job verify-model` only checks the model volume against the pinned
// manifest and exits 0 (verified), 10 (not provisioned) or 20 (unapproved or
// modified). The shell scripts call it through verify-approved-model.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "verify-model" {
		os.Exit(verifyModelCommand(os.Args[2:]))
	}
	if len(os.Args) == 2 && os.Args[1] == "write-approved-manifest" {
		model, err := loadApprovedModel(defaultApprovedModelConfig)
		if err == nil {
			err = model.writeManifest(defaultModelRoot)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "Write approved manifest:", err)
			os.Exit(verifyInvalid)
		}
		return
	}

	requestPath := flag.String("request", "/job/input/request.json", "request file")
	outputDir := flag.String("output", "/job/output", "directory that receives response.json")
	approvedConfig := flag.String("approved-model", defaultApprovedModelConfig, "approved model configuration")
	modelRoot := flag.String("models", defaultModelRoot, "model directory")
	ollamaPath := flag.String("ollama", "/bin/ollama", "ollama executable")
	host := flag.String("host", "127.0.0.1:11434", "loopback address for the internal Ollama server")
	startTimeout := flag.Duration("start-timeout", 60*time.Second, "time allowed for Ollama to become ready")
	jobTimeout := flag.Duration("timeout", 10*time.Minute, "time allowed for generation")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	var store string
	r := &runner{
		config: config{
			// The entrypoint sets this from the approved-model.env baked into
			// the image, overriding any value passed at runtime.
			model:        os.Getenv("APPROVED_MODEL"),
			requestPath:  *requestPath,
			outputDir:    *outputDir,
			startTimeout: *startTimeout,
			jobTimeout:   *jobTimeout,
			requireGPU:   os.Getenv("OLLAMA_JOB_REQUIRE_GPU") == "1",
		},
		verify: func(context.Context) int { return verifyApprovedModel(*approvedConfig, *modelRoot, os.Stderr) },
		start: func() (server, error) {
			model, err := loadApprovedModel(*approvedConfig)
			if err != nil {
				return nil, err
			}
			store, err = os.MkdirTemp("", "ollama-model-store-")
			if err != nil {
				return nil, err
			}
			if err := stageModelStore(store, *modelRoot, model); err != nil {
				return nil, err
			}
			return startServer(*ollamaPath, *host, store)
		},
		client: newOllamaClient("http://" + *host),
		stderr: os.Stderr,
	}
	code := r.run(ctx)
	if store != "" {
		_ = os.RemoveAll(store)
	}
	stop()
	os.Exit(code)
}

func verifyModelCommand(args []string) int {
	flags := flag.NewFlagSet("verify-model", flag.ContinueOnError)
	approvedConfig := flags.String("approved-model", defaultApprovedModelConfig, "approved model configuration")
	modelRoot := flags.String("models", defaultModelRoot, "model directory")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return verifyInvalid
	}
	return verifyApprovedModel(*approvedConfig, *modelRoot, os.Stderr)
}
