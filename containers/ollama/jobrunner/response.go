package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"
)

const (
	responseFileName = "response.json"
	maxResponseBytes = 64 << 10
)

type successResponse struct {
	Status     string  `json:"status"`
	Model      string  `json:"model"`
	Response   string  `json:"response"`
	DoneReason string  `json:"done_reason"`
	Truncated  bool    `json:"truncated"`
	Processor  string  `json:"processor"`
	Metrics    metrics `json:"metrics"`
}

type metrics struct {
	PromptTokens     int   `json:"prompt_tokens"`
	CompletionTokens int   `json:"completion_tokens"`
	TotalDurationMS  int64 `json:"total_duration_ms"`
}

type errorResponse struct {
	Status string      `json:"status"`
	Model  string      `json:"model"`
	Error  errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// truncateUTF8 shortens text to at most limit bytes without splitting a
// multi-byte character.
func truncateUTF8(text string, limit int) (string, bool) {
	if len(text) <= limit {
		return text, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true
}

// writeResponse replaces dir/response.json atomically, so the node agent
// never reads a partially written result.
func writeResponse(dir string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	data = append(data, '\n')

	temp, err := os.CreateTemp(dir, ".response-*.json")
	if err != nil {
		return fmt.Errorf("create response file: %w", err)
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write response file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync response file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close response file: %w", err)
	}
	// The node agent reads the result as a different user than the container.
	if err := os.Chmod(tempPath, 0o644); err != nil {
		return fmt.Errorf("set response file mode: %w", err)
	}
	if err := os.Rename(tempPath, filepath.Join(dir, responseFileName)); err != nil {
		return fmt.Errorf("publish response file: %w", err)
	}
	committed = true
	return nil
}
