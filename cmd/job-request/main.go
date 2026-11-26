// Command job-request submits a job and prints the provider's decision and
// terminal result as JSON lines.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/jobpush"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	address := flag.String("url", "http://127.0.0.1:8081", "provider job API base URL")
	digest := flag.String("digest", "", "approved image digest for a generic job")
	prompt := flag.String("prompt", "", "submit an Ollama prompt")
	promptFile := flag.String("prompt-file", "", "read the Ollama prompt from a file or - for stdin")
	numPredict := flag.Int("num-predict", 0, "maximum generated Ollama tokens (1–1024, 0 uses the model default)")
	seconds := flag.Int("seconds", jobpush.DefaultSeconds, "maximum job time in seconds (1–3600)")
	flag.Parse()
	if flag.NArg() != 0 || *seconds < 1 || *seconds > jobpush.MaxSeconds {
		return errors.New("use -help for flags and set -seconds between 1 and 3600")
	}
	hasPrompt := *prompt != "" || *promptFile != ""
	if (*prompt != "" && *promptFile != "") || (*digest != "") == hasPrompt {
		return errors.New("specify exactly one of -digest, -prompt or -prompt-file")
	}
	if *numPredict < 0 || *numPredict > lease.MaxNumPredict || (*numPredict != 0 && *digest != "") {
		return errors.New("-num-predict must be between 1 and 1024 for Ollama jobs")
	}

	token := os.Getenv(jobpush.TokenEnv)
	if err := jobpush.ValidateToken(token); err != nil {
		return err
	}
	endpoint, err := jobURL(*address)
	if err != nil {
		return err
	}
	submission := jobpush.Submission{ImageDigest: *digest, Seconds: *seconds}
	if *digest == "" {
		text := *prompt
		if *promptFile != "" {
			if text, err = readPrompt(*promptFile); err != nil {
				return err
			}
		}
		submission.Inference = &lease.InferenceRequest{Prompt: text}
		if *numPredict != 0 {
			submission.Inference.Options = &lease.InferenceOptions{NumPredict: numPredict}
		}
		if _, err := submission.Inference.MarshalInput(); err != nil {
			return fmt.Errorf("invalid Ollama request: %w", err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(*seconds)*time.Second+time.Minute)
	defer cancel()
	return submit(ctx, endpoint, token, submission, os.Stdout)
}

func readPrompt(path string) (string, error) {
	var reader io.Reader = os.Stdin
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer file.Close()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(reader, lease.MaxPromptBytes+1))
	return string(data), err
}

func jobURL(address string) (string, error) {
	endpoint, err := url.Parse(address)
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" ||
		endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return "", errors.New("-url must be a provider base URL")
	}
	if endpoint.Scheme != "https" && (endpoint.Scheme != "http" || !jobpush.IsLoopbackIP(endpoint.Hostname())) {
		return "", errors.New("-url requires HTTPS outside a literal loopback address")
	}
	endpoint.Path = jobpush.Path
	return endpoint.String(), nil
}

func submit(ctx context.Context, endpoint, token string, submission jobpush.Submission, output io.Writer) error {
	payload, err := json.Marshal(submission)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("submit job: %w", err)
	}
	defer response.Body.Close()
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || contentType != jobpush.MediaType {
		return fmt.Errorf("unexpected provider reply (%s)", response.Status)
	}

	// Output may expand when re-encoded as JSON. Bound the whole event stream.
	decoder := json.NewDecoder(io.LimitReader(response.Body, 6*lease.MaxResponseFileBytes+(16<<10)))
	encoder := json.NewEncoder(output)
	var decision jobpush.Event
	if err := decoder.Decode(&decision); err != nil {
		return fmt.Errorf("read job decision: %w", err)
	}
	if err := encoder.Encode(decision); err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || decision.Type != "accepted" || decision.LeaseID == "" {
		return fmt.Errorf("provider returned %s (%s)", decision.Type, response.Status)
	}

	var result jobpush.Event
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("read result for %s: %w", decision.LeaseID, err)
	}
	if result.LeaseID != decision.LeaseID || (result.Type != "result" && result.Type != "error") {
		return errors.New("unexpected terminal event from provider")
	}
	if err := encoder.Encode(result); err != nil {
		return err
	}
	if result.Type == "error" {
		return fmt.Errorf("provider could not return a result for %s", result.LeaseID)
	}
	if result.Outcome != lease.OutcomeCompleted {
		return fmt.Errorf("job %s ended with outcome %s", result.LeaseID, result.Outcome)
	}
	return nil
}
