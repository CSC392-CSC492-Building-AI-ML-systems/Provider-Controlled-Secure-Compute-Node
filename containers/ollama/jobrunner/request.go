package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Limits from JOB_CONTRACT.md.
const (
	maxRequestBytes   = 16 << 10
	maxPromptBytes    = 8 << 10
	defaultNumPredict = 256
	maxNumPredict     = 1024
)

// Request is the job input read from /job/input/request.json.
type Request struct {
	Prompt  string   `json:"prompt"`
	Options *Options `json:"options,omitempty"`
}

// Options are the only generation settings a request may change.
type Options struct {
	NumPredict  *int     `json:"num_predict,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty"`
	Seed        *int     `json:"seed,omitempty"`
}

// invalidRequestError marks a request the job cannot run. Its message must
// never include prompt text.
type invalidRequestError struct{ message string }

func (err *invalidRequestError) Error() string { return err.message }

func invalidf(format string, args ...any) error {
	return &invalidRequestError{message: fmt.Sprintf(format, args...)}
}

func parseRequest(reader io.Reader) (Request, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxRequestBytes+1))
	if err != nil {
		return Request{}, invalidf("request could not be read: %v", err)
	}
	if len(data) > maxRequestBytes {
		return Request{}, invalidf("request is larger than %d bytes", maxRequestBytes)
	}
	if !utf8.Valid(data) {
		return Request{}, invalidf("request is not valid UTF-8")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return Request{}, invalidf("request is not a valid JSON object: %v", jsonProblem(err))
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Request{}, invalidf("request must contain exactly one JSON object")
	}
	if err := request.validate(); err != nil {
		return Request{}, err
	}
	return request, nil
}

// jsonProblem describes a decoding error without echoing request contents.
func jsonProblem(err error) string {
	var syntax *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntax):
		return fmt.Sprintf("syntax error at byte %d", syntax.Offset)
	case errors.As(err, &typeErr):
		return fmt.Sprintf("field %q has the wrong type", typeErr.Field)
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected end of input"
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return strings.TrimPrefix(err.Error(), "json: ")
	default:
		return "malformed JSON"
	}
}

func (request Request) validate() error {
	if strings.TrimSpace(request.Prompt) == "" {
		return invalidf("prompt is required")
	}
	if len(request.Prompt) > maxPromptBytes {
		return invalidf("prompt is larger than %d bytes", maxPromptBytes)
	}
	if strings.ContainsRune(request.Prompt, 0) {
		return invalidf("prompt contains a NUL character")
	}

	options := request.Options
	if options == nil {
		return nil
	}
	if value := options.NumPredict; value != nil && (*value < 1 || *value > maxNumPredict) {
		return invalidf("options.num_predict must be between 1 and %d", maxNumPredict)
	}
	if value := options.Temperature; value != nil && (*value < 0 || *value > 2) {
		return invalidf("options.temperature must be between 0 and 2")
	}
	if value := options.TopP; value != nil && (*value <= 0 || *value > 1) {
		return invalidf("options.top_p must be greater than 0 and at most 1")
	}
	if value := options.TopK; value != nil && (*value < 1 || *value > 100) {
		return invalidf("options.top_k must be between 1 and 100")
	}
	return nil
}

// ollamaOptions converts the request into Ollama's options object. Only
// validated fields are passed through, and num_predict always has a bound.
func (request Request) ollamaOptions() map[string]any {
	options := map[string]any{"num_predict": defaultNumPredict}
	if request.Options == nil {
		return options
	}
	if value := request.Options.NumPredict; value != nil {
		options["num_predict"] = *value
	}
	if value := request.Options.Temperature; value != nil {
		options["temperature"] = *value
	}
	if value := request.Options.TopP; value != nil {
		options["top_p"] = *value
	}
	if value := request.Options.TopK; value != nil {
		options["top_k"] = *value
	}
	if value := request.Options.Seed; value != nil {
		options["seed"] = *value
	}
	return options
}
