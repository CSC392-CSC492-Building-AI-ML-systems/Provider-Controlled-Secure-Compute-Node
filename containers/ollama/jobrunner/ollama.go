package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxOllamaResponseBytes bounds what is read from Ollama. A response is at
// most 1,024 tokens, so this leaves ample room for JSON escaping.
const maxOllamaResponseBytes = 1 << 20

type ollamaClient struct {
	baseURL string
	http    *http.Client
}

func newOllamaClient(baseURL string) *ollamaClient {
	return &ollamaClient{baseURL: baseURL, http: &http.Client{}}
}

type generateResult struct {
	Response        string `json:"response"`
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason"`
	TotalDuration   int64  `json:"total_duration"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
}

// waitReady polls Ollama until it answers, the server exits, or the timeout
// passes.
func (client *ollamaClient) waitReady(ctx context.Context, exited <-chan error, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if client.ping(ctx) == nil {
			return nil
		}
		select {
		case err := <-exited:
			return fmt.Errorf("ollama exited before it was ready: %v", err)
		case <-ctx.Done():
			return fmt.Errorf("ollama was not ready after %s", timeout)
		case <-ticker.C:
		}
	}
}

func (client *ollamaClient) ping(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/api/version", nil)
	if err != nil {
		return err
	}
	response, err := client.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", response.StatusCode)
	}
	return nil
}

func (client *ollamaClient) generate(ctx context.Context, model, prompt string, options map[string]any) (generateResult, error) {
	body, err := json.Marshal(map[string]any{
		"model":   model,
		"prompt":  prompt,
		"stream":  false,
		"options": options,
	})
	if err != nil {
		return generateResult{}, err
	}
	var result generateResult
	if err := client.call(ctx, http.MethodPost, "/api/generate", body, &result); err != nil {
		return generateResult{}, err
	}
	if !result.Done {
		return generateResult{}, errors.New("ollama returned an incomplete generation")
	}
	return result, nil
}

// processor reports where the loaded model runs, based on how much of it is
// held in GPU memory.
func (client *ollamaClient) processor(ctx context.Context, model string) string {
	var status struct {
		Models []struct {
			Name     string `json:"name"`
			Model    string `json:"model"`
			Size     int64  `json:"size"`
			SizeVRAM int64  `json:"size_vram"`
		} `json:"models"`
	}
	if err := client.call(ctx, http.MethodGet, "/api/ps", nil, &status); err != nil {
		return "unknown"
	}
	for _, loaded := range status.Models {
		if loaded.Name != model && loaded.Model != model {
			continue
		}
		switch {
		case loaded.Size <= 0:
			return "unknown"
		case loaded.SizeVRAM <= 0:
			return "cpu"
		case loaded.SizeVRAM >= loaded.Size:
			return "gpu"
		default:
			return "partial"
		}
	}
	return "unknown"
}

func (client *ollamaClient) call(ctx context.Context, method, path string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, maxOllamaResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read ollama response: %w", err)
	}
	if len(data) > maxOllamaResponseBytes {
		return errors.New("ollama response is too large")
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &failure) == nil && failure.Error != "" {
			return fmt.Errorf("ollama returned status %d: %s", response.StatusCode, failure.Error)
		}
		return fmt.Errorf("ollama returned status %d", response.StatusCode)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode ollama response: %w", err)
	}
	return nil
}
