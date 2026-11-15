package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const testModel = "tinyllama:1.1b"

type fakeServer struct {
	done    chan error
	stopped bool
}

func (server *fakeServer) exited() <-chan error { return server.done }
func (server *fakeServer) stop()                { server.stopped = true }

// fakeOllama answers the endpoints the runner uses.
type fakeOllama struct {
	generateStatus int
	generateBody   string
	sizeVRAM       int64
	gotPrompt      string
	gotModel       string
	gotOptions     map[string]any
}

func (fake *fakeOllama) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":"test"}`))
	})
	mux.HandleFunc("/api/generate", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model   string         `json:"model"`
			Prompt  string         `json:"prompt"`
			Stream  bool           `json:"stream"`
			Options map[string]any `json:"options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode generate request: %v", err)
		}
		if body.Stream {
			t.Error("generate request asked for streaming")
		}
		fake.gotModel, fake.gotPrompt, fake.gotOptions = body.Model, body.Prompt, body.Options
		if fake.generateStatus != 0 {
			w.WriteHeader(fake.generateStatus)
		}
		_, _ = w.Write([]byte(fake.generateBody))
	})
	mux.HandleFunc("/api/ps", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{
			{"name": testModel, "model": testModel, "size": 1000, "size_vram": fake.sizeVRAM},
		}})
	})
	return mux
}

type harness struct {
	runner   *runner
	ollama   *fakeOllama
	server   *fakeServer
	output   string
	stderr   *bytes.Buffer
	verified bool
	started  bool
}

func newHarness(t *testing.T, request string) *harness {
	t.Helper()
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.json")
	if request != "" {
		if err := os.WriteFile(requestPath, []byte(request), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(dir, "output")
	if err := os.Mkdir(output, 0o755); err != nil {
		t.Fatal(err)
	}

	h := &harness{
		ollama: &fakeOllama{generateBody: `{"response":"A GPU is a processor.","done":true,"done_reason":"stop","total_duration":2500000000,"prompt_eval_count":12,"eval_count":6}`},
		server: &fakeServer{done: make(chan error)},
		output: output,
		stderr: &bytes.Buffer{},
	}
	httpServer := httptest.NewServer(h.ollama.handler(t))
	t.Cleanup(httpServer.Close)

	h.runner = &runner{
		config: config{
			model:        testModel,
			requestPath:  requestPath,
			outputDir:    output,
			startTimeout: 2 * time.Second,
			jobTimeout:   5 * time.Second,
		},
		verify: func(context.Context) int { h.verified = true; return 0 },
		start:  func() (server, error) { h.started = true; return h.server, nil },
		client: newOllamaClient(httpServer.URL),
		stderr: h.stderr,
	}
	return h
}

func (h *harness) response(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(h.output, responseFileName))
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var response map[string]any
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response
}

func errorCode(response map[string]any) string {
	detail, _ := response["error"].(map[string]any)
	code, _ := detail["code"].(string)
	return code
}

func TestRunWritesSuccessfulResponse(t *testing.T) {
	h := newHarness(t, `{"prompt":"What is a GPU?","options":{"num_predict":32}}`)

	if code := h.runner.run(context.Background()); code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitOK, h.stderr)
	}
	response := h.response(t)
	if response["status"] != "ok" || response["model"] != testModel {
		t.Fatalf("response = %v", response)
	}
	if response["response"] != "A GPU is a processor." || response["done_reason"] != "stop" {
		t.Fatalf("response = %v", response)
	}
	if response["processor"] != "cpu" || response["truncated"] != false {
		t.Fatalf("response = %v", response)
	}
	metrics := response["metrics"].(map[string]any)
	if metrics["prompt_tokens"] != float64(12) || metrics["completion_tokens"] != float64(6) || metrics["total_duration_ms"] != float64(2500) {
		t.Fatalf("metrics = %v", metrics)
	}
	if h.ollama.gotModel != testModel || h.ollama.gotPrompt != "What is a GPU?" {
		t.Fatalf("generate called with model %q prompt %q", h.ollama.gotModel, h.ollama.gotPrompt)
	}
	if h.ollama.gotOptions["num_predict"] != float64(32) {
		t.Fatalf("options = %v", h.ollama.gotOptions)
	}
	if !h.server.stopped {
		t.Fatal("ollama server was not stopped")
	}
	if info, err := os.Stat(filepath.Join(h.output, responseFileName)); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("response file mode = %v, err %v", info.Mode().Perm(), err)
	}
	entries, _ := os.ReadDir(h.output)
	if len(entries) != 1 {
		t.Fatalf("output directory has %d entries, want only response.json", len(entries))
	}
}

func TestRunReportsGPUProcessor(t *testing.T) {
	h := newHarness(t, `{"prompt":"hi"}`)
	h.ollama.sizeVRAM = 1000
	if code := h.runner.run(context.Background()); code != exitOK {
		t.Fatalf("exit code = %d; stderr: %s", code, h.stderr)
	}
	if got := h.response(t)["processor"]; got != "gpu" {
		t.Fatalf("processor = %v, want gpu", got)
	}
}

func TestRunRejectsInvalidRequestBeforeStartingOllama(t *testing.T) {
	for name, request := range map[string]string{
		"missing file":   "",
		"malformed json": `{"prompt":`,
		"model override": `{"prompt":"hi","model":"llama3"}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, request)
			if code := h.runner.run(context.Background()); code != exitInvalidRequest {
				t.Fatalf("exit code = %d, want %d", code, exitInvalidRequest)
			}
			if got := errorCode(h.response(t)); got != "invalid_request" {
				t.Fatalf("error code = %q", got)
			}
			if h.verified || h.started {
				t.Fatal("invalid request reached model verification or Ollama")
			}
		})
	}
}

func TestRunMapsModelVerificationFailures(t *testing.T) {
	tests := []struct {
		status   int
		wantExit int
		wantCode string
	}{
		{status: 10, wantExit: exitModelMissing, wantCode: "model_not_provisioned"},
		{status: 20, wantExit: exitModelInvalid, wantCode: "model_verification_failed"},
		{status: 1, wantExit: exitModelInvalid, wantCode: "model_verification_failed"},
	}
	for _, test := range tests {
		h := newHarness(t, `{"prompt":"hi"}`)
		h.runner.verify = func(context.Context) int { return test.status }
		if code := h.runner.run(context.Background()); code != test.wantExit {
			t.Fatalf("verify status %d: exit code = %d, want %d", test.status, code, test.wantExit)
		}
		if got := errorCode(h.response(t)); got != test.wantCode {
			t.Fatalf("verify status %d: error code = %q, want %q", test.status, got, test.wantCode)
		}
		if h.started {
			t.Fatal("ollama started with an unverified model")
		}
	}
}

func TestRunReportsServerThatExitsEarly(t *testing.T) {
	h := newHarness(t, `{"prompt":"hi"}`)
	h.runner.client = newOllamaClient("http://127.0.0.1:1")
	h.server.done = make(chan error, 1)
	h.server.done <- os.ErrProcessDone
	if code := h.runner.run(context.Background()); code != exitServerStartFailed {
		t.Fatalf("exit code = %d, want %d", code, exitServerStartFailed)
	}
	if got := errorCode(h.response(t)); got != "server_start_failed" {
		t.Fatalf("error code = %q", got)
	}
}

func TestRunReportsInferenceFailures(t *testing.T) {
	tests := map[string]struct {
		status int
		body   string
	}{
		"ollama error":   {status: http.StatusInternalServerError, body: `{"error":"model failed to load"}`},
		"empty response": {body: `{"response":"  ","done":true}`},
		"not done":       {body: `{"response":"partial","done":false}`},
		"bad json":       {body: `not json`},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, `{"prompt":"hi"}`)
			h.ollama.generateStatus, h.ollama.generateBody = test.status, test.body
			if code := h.runner.run(context.Background()); code != exitInferenceFailed {
				t.Fatalf("exit code = %d, want %d", code, exitInferenceFailed)
			}
			if got := errorCode(h.response(t)); got != "inference_failed" {
				t.Fatalf("error code = %q", got)
			}
		})
	}
}

func TestRunReportsCancellation(t *testing.T) {
	h := newHarness(t, `{"prompt":"hi"}`)
	ctx, cancel := context.WithCancel(context.Background())
	h.runner.verify = func(context.Context) int { cancel(); return 0 }
	if code := h.runner.run(ctx); code != exitCancelled {
		t.Fatalf("exit code = %d, want %d", code, exitCancelled)
	}
	if got := errorCode(h.response(t)); got != "cancelled" {
		t.Fatalf("error code = %q", got)
	}
}

func TestRunFailsWithoutOutputDirectory(t *testing.T) {
	h := newHarness(t, `{"prompt":"hi"}`)
	h.runner.config.outputDir = filepath.Join(t.TempDir(), "missing")
	if code := h.runner.run(context.Background()); code != exitOutputFailed {
		t.Fatalf("exit code = %d, want %d", code, exitOutputFailed)
	}
}

func TestRunNeverLogsPromptOrResponse(t *testing.T) {
	h := newHarness(t, `{"prompt":"secret prompt text"}`)
	h.ollama.generateBody = `{"response":"secret generated text","done":true}`
	if code := h.runner.run(context.Background()); code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	if log := h.stderr.String(); strings.Contains(log, "secret") {
		t.Fatalf("stderr contains job text: %s", log)
	}
}

func TestTruncateUTF8KeepsWholeCharacters(t *testing.T) {
	text := strings.Repeat("é", 10) // 20 bytes
	got, truncated := truncateUTF8(text, 5)
	if !truncated || got != "éé" || !utf8.ValidString(got) {
		t.Fatalf("truncateUTF8 = %q, %v", got, truncated)
	}
	if got, truncated := truncateUTF8("short", 10); truncated || got != "short" {
		t.Fatalf("truncateUTF8 = %q, %v", got, truncated)
	}
}

func TestServerEnvPinsSettings(t *testing.T) {
	env := serverEnv([]string{
		"PATH=/bin",
		"OLLAMA_DEBUG=1",
		"OLLAMA_HOST=0.0.0.0:11434",
		"OLLAMA_MODELS=/elsewhere",
	}, "127.0.0.1:11434")
	joined := strings.Join(env, "\n")
	for _, want := range []string{"PATH=/bin", "OLLAMA_HOST=127.0.0.1:11434", "OLLAMA_MODELS=/models", "OLLAMA_NOPRUNE=1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("env missing %s: %v", want, env)
		}
	}
	for _, unwanted := range []string{"OLLAMA_DEBUG", "0.0.0.0", "/elsewhere"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("env contains %s: %v", unwanted, env)
		}
	}
}
