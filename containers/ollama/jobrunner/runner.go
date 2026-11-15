package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Exit codes from JOB_CONTRACT.md.
const (
	exitOK                = 0
	exitInvalidRequest    = 2
	exitModelMissing      = 10
	exitModelInvalid      = 20
	exitServerStartFailed = 30
	exitInferenceFailed   = 31
	exitOutputFailed      = 40
	exitCancelled         = 143
)

type config struct {
	model        string
	requestPath  string
	outputDir    string
	startTimeout time.Duration
	jobTimeout   time.Duration
}

type runner struct {
	config config
	// verify runs the model check and returns its exit status.
	verify func(ctx context.Context) int
	start  func() (server, error)
	client *ollamaClient
	stderr io.Writer
}

// verifyCommand runs verify-approved-model, whose exit status 10 means the
// model is missing and any other failure means it is invalid.
func verifyCommand(path string) func(ctx context.Context) int {
	return func(ctx context.Context) int {
		cmd := exec.CommandContext(ctx, path)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if err == nil {
			return 0
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "could not run model verification: %v\n", err)
		return exitModelInvalid
	}
}

func (r *runner) run(ctx context.Context) int {
	if info, err := os.Stat(r.config.outputDir); err != nil || !info.IsDir() {
		fmt.Fprintf(r.stderr, "output directory %s is not available\n", r.config.outputDir)
		return exitOutputFailed
	}
	if r.config.model == "" {
		return r.fail("model_verification_failed", "approved model is not configured", exitModelInvalid)
	}

	request, err := r.readRequest()
	if err != nil {
		return r.fail("invalid_request", err.Error(), exitInvalidRequest)
	}

	switch status := r.verify(ctx); status {
	case 0:
	case exitModelMissing:
		return r.fail("model_not_provisioned", "approved model "+r.config.model+" is not provisioned", exitModelMissing)
	default:
		return r.fail("model_verification_failed", "model volume does not match the approved model", exitModelInvalid)
	}
	if ctx.Err() != nil {
		return r.cancelled()
	}

	process, err := r.start()
	if err != nil {
		return r.fail("server_start_failed", "ollama could not be started: "+err.Error(), exitServerStartFailed)
	}
	defer process.stop()

	if err := r.client.waitReady(ctx, process.exited(), r.config.startTimeout); err != nil {
		if ctx.Err() != nil {
			return r.cancelled()
		}
		return r.fail("server_start_failed", err.Error(), exitServerStartFailed)
	}

	jobCtx, cancel := context.WithTimeout(ctx, r.config.jobTimeout)
	defer cancel()
	result, err := r.client.generate(jobCtx, r.config.model, request.Prompt, request.ollamaOptions())
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return r.cancelled()
		case jobCtx.Err() != nil:
			return r.fail("inference_failed", fmt.Sprintf("generation did not finish within %s", r.config.jobTimeout), exitInferenceFailed)
		default:
			return r.fail("inference_failed", err.Error(), exitInferenceFailed)
		}
	}
	if strings.TrimSpace(result.Response) == "" {
		return r.fail("inference_failed", "model returned an empty response", exitInferenceFailed)
	}

	text, truncated := truncateUTF8(result.Response, maxResponseBytes)
	response := successResponse{
		Status:     "ok",
		Model:      r.config.model,
		Response:   text,
		DoneReason: result.DoneReason,
		Truncated:  truncated,
		Processor:  r.client.processor(ctx, r.config.model),
		Metrics: metrics{
			PromptTokens:     result.PromptEvalCount,
			CompletionTokens: result.EvalCount,
			TotalDurationMS:  result.TotalDuration / int64(time.Millisecond),
		},
	}
	if err := writeResponse(r.config.outputDir, response); err != nil {
		fmt.Fprintf(r.stderr, "job succeeded but the result could not be saved: %v\n", err)
		return exitOutputFailed
	}
	fmt.Fprintf(r.stderr, "job completed: %d prompt tokens, %d generated tokens, processor %s\n",
		response.Metrics.PromptTokens, response.Metrics.CompletionTokens, response.Processor)
	return exitOK
}

func (r *runner) readRequest() (Request, error) {
	file, err := os.Open(r.config.requestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Request{}, invalidf("request file %s is missing", r.config.requestPath)
		}
		return Request{}, invalidf("request file %s could not be opened", r.config.requestPath)
	}
	defer file.Close()
	return parseRequest(file)
}

func (r *runner) cancelled() int {
	return r.fail("cancelled", "job was stopped before it finished", exitCancelled)
}

// fail records an error result. Messages are written to stderr and to the
// response, so they must never contain prompt or generated text.
func (r *runner) fail(code, message string, exitCode int) int {
	fmt.Fprintf(r.stderr, "job failed (%s): %s\n", code, message)
	response := errorResponse{
		Status: "error",
		Model:  r.config.model,
		Error:  errorDetail{Code: code, Message: message},
	}
	if err := writeResponse(r.config.outputDir, response); err != nil {
		fmt.Fprintf(r.stderr, "error result could not be saved: %v\n", err)
		return exitOutputFailed
	}
	return exitCode
}
