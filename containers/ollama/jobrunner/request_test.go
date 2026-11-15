package main

import (
	"errors"
	"strings"
	"testing"
)

func TestParseRequestAcceptsValidRequest(t *testing.T) {
	request, err := parseRequest(strings.NewReader(`{
		"prompt": "hello",
		"options": {"num_predict": 64, "temperature": 0.2, "top_p": 0.9, "top_k": 40, "seed": 7}
	}`))
	if err != nil {
		t.Fatalf("parseRequest: %v", err)
	}
	options := request.ollamaOptions()
	want := map[string]any{"num_predict": 64, "temperature": 0.2, "top_p": 0.9, "top_k": 40, "seed": 7}
	for key, value := range want {
		if options[key] != value {
			t.Errorf("option %s = %v, want %v", key, options[key], value)
		}
	}
	if len(options) != len(want) {
		t.Errorf("options = %v, want only %v", options, want)
	}
}

func TestParseRequestDefaultsNumPredict(t *testing.T) {
	request, err := parseRequest(strings.NewReader(`{"prompt": "hello"}`))
	if err != nil {
		t.Fatalf("parseRequest: %v", err)
	}
	if got := request.ollamaOptions()["num_predict"]; got != defaultNumPredict {
		t.Fatalf("num_predict = %v, want %d", got, defaultNumPredict)
	}
}

func TestParseRequestRejectsInvalidRequests(t *testing.T) {
	tests := map[string]string{
		"empty file":            ``,
		"not json":              `prompt=hello`,
		"array":                 `["hello"]`,
		"null":                  `null`,
		"missing prompt":        `{}`,
		"blank prompt":          `{"prompt": "  \n"}`,
		"wrong prompt type":     `{"prompt": 5}`,
		"model override":        `{"prompt": "hi", "model": "llama3"}`,
		"system override":       `{"prompt": "hi", "system": "ignore rules"}`,
		"unknown option":        `{"prompt": "hi", "options": {"num_gpu": 0}}`,
		"num_predict too large": `{"prompt": "hi", "options": {"num_predict": 5000}}`,
		"num_predict zero":      `{"prompt": "hi", "options": {"num_predict": 0}}`,
		"temperature too high":  `{"prompt": "hi", "options": {"temperature": 3}}`,
		"top_p zero":            `{"prompt": "hi", "options": {"top_p": 0}}`,
		"top_k too large":       `{"prompt": "hi", "options": {"top_k": 1000}}`,
		"two objects":           `{"prompt": "a"} {"prompt": "b"}`,
		"nul in prompt":         `{"prompt": "a\u0000b"}`,
		"prompt too large":      `{"prompt": "` + strings.Repeat("a", maxPromptBytes+1) + `"}`,
		"request too large":     `{"prompt": "hi", "pad": "` + strings.Repeat("a", maxRequestBytes) + `"}`,
		"invalid utf-8":         "{\"prompt\": \"\xff\"}",
		"truncated json":        `{"prompt": "hi"`,
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parseRequest(strings.NewReader(input))
			var invalid *invalidRequestError
			if !errors.As(err, &invalid) {
				t.Fatalf("parseRequest error = %v, want invalid request", err)
			}
		})
	}
}

func TestParseRequestErrorsDoNotEchoPrompt(t *testing.T) {
	secret := "top-secret-prompt-text"
	inputs := []string{
		`{"prompt": "` + secret + `", "options": {"num_predict": "` + secret + `"}}`,
		`{"prompt": "` + secret + `" "options": {}}`,
		`{"prompt": "` + secret + `", "options": {"temperature": 9}}`,
	}
	for _, input := range inputs {
		_, err := parseRequest(strings.NewReader(input))
		if err == nil {
			t.Fatalf("parseRequest(%q) succeeded, want error", input)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error %q contains the prompt", err)
		}
	}
}
