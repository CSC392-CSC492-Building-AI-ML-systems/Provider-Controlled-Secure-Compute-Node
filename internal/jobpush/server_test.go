package jobpush

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/allowlist"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

const (
	token  = "test-token-1234567890-abcdefghijkl"
	digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

type testRunner func(string, *lease.InferenceRequest) (lease.Workload, error)

func (runner testRunner) Launch(_ context.Context, digest string) (lease.Workload, error) {
	return runner(digest, nil)
}

func (runner testRunner) LaunchRequest(_ context.Context, digest string, request *lease.InferenceRequest) (lease.Workload, error) {
	return runner(digest, request)
}

type testWorkload struct {
	finish   <-chan struct{}
	code     int
	response *lease.InferenceResponse
}

func (workload *testWorkload) Wait(ctx context.Context) (int, error) {
	if workload.finish != nil {
		select {
		case <-workload.finish:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return workload.code, nil
}

func (workload *testWorkload) Kill(context.Context) error { return nil }

func (workload *testWorkload) Response() (lease.InferenceResponse, bool) {
	if workload.response == nil {
		return lease.InferenceResponse{}, false
	}
	return *workload.response, true
}

type testRecorder chan lease.Result

func (testRecorder) ReportAccepted(context.Context, string) error                        { return nil }
func (testRecorder) ReportRejected(context.Context, string, lease.RejectionReason) error { return nil }
func (recorder testRecorder) ReportResult(_ context.Context, result lease.Result) error {
	recorder <- result
	return nil
}

func newTestServer(t *testing.T, runner testRunner, ollama string) (*httptest.Server, *lease.Manager, testRecorder) {
	t.Helper()
	checker, err := allowlist.NewChecker([]allowlist.Entry{{Image: "registry.example/jobs/test", Digest: digest}})
	if err != nil {
		t.Fatal(err)
	}
	recorder := make(testRecorder, 4)
	reporter := &Reporter{Coordinator: recorder}
	manager := lease.NewManager(checker, runner, reporter)
	server := httptest.NewServer(Handler(manager, reporter, token, ollama, true, log.New(io.Discard, "", 0)))
	server.Client().Timeout = 5 * time.Second
	t.Cleanup(server.Close)
	return server, manager, recorder
}

func post(t *testing.T, server *httptest.Server, key, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, server.URL+Path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func nextEvent(t *testing.T, decoder *json.Decoder) Event {
	t.Helper()
	var event Event
	if err := decoder.Decode(&event); err != nil {
		t.Fatal(err)
	}
	return event
}

func expectReject(t *testing.T, response *http.Response, code int, reason string) {
	t.Helper()
	defer response.Body.Close()
	event := nextEvent(t, json.NewDecoder(response.Body))
	if response.StatusCode != code || event.Reason != reason {
		t.Fatalf("status=%d, event=%+v, want status=%d reason=%s", response.StatusCode, event, code, reason)
	}
}

func TestRequesterRejectsBeforeLaunch(t *testing.T) {
	server, _, _ := newTestServer(t, func(string, *lease.InferenceRequest) (lease.Workload, error) {
		t.Error("rejected request called the container engine")
		return &testWorkload{}, nil
	}, "")
	for _, test := range []struct {
		name, key, request, reason string
		code                       int
	}{
		{"missing token", "", `{}`, "unauthorized", 401},
		{"wrong token", "another-token", `{}`, "unauthorized", 401},
		{"no workload", token, `{}`, "invalid_offer", 400},
		{"both workloads", token, `{"image_digest":"some","inference":{"prompt":"test"}}`, "invalid_offer", 400},
		{"unknown digest", token, `{"image_digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}`, "digest_not_allowed", 403},
		{"container parameters", token, `{"image_digest":"` + digest + `","command":["echo"]}`, "invalid_offer", 400},
		{"requester GPU", token, `{"inference":{"prompt":"test","require_gpu":true}}`, "invalid_offer", 400},
		{"unknown Ollama option", token, `{"inference":{"prompt":"test","options":{"num_gpu":10}}}`, "invalid_offer", 400},
		{"blank prompt", token, `{"inference":{"prompt":" "}}`, "invalid_inference_request", 400},
		{"oversized request file", token, `{"inference":{"prompt":"` + strings.Repeat("<", 8192) + `"}}`, "invalid_inference_request", 400},
		{"Ollama disabled", token, `{"inference":{"prompt":"test"}}`, "inference_disabled", 403},
		{"runtime too long", token, `{"image_digest":"` + digest + `","seconds":3601}`, "invalid_offer", 400},
		{"body too large", token, strings.Repeat(" ", maxBodyBytes+1), "request_too_large", 413},
		{"two objects", token, `{"image_digest":"` + digest + `"}{}`, "invalid_offer", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			expectReject(t, post(t, server, test.key, test.request), test.code, test.reason)
		})
	}
}

func TestRequesterStreamsAndHonorsDrain(t *testing.T) {
	finish := make(chan struct{})
	server, manager, _ := newTestServer(t, func(gotDigest string, request *lease.InferenceRequest) (lease.Workload, error) {
		if gotDigest != digest || request != nil {
			t.Errorf("unexpected generic launch: %q, %+v", gotDigest, request)
		}
		return &testWorkload{finish: finish, code: 17}, nil
	}, "")
	body := `{"image_digest":"` + digest + `","seconds":10}`
	response := post(t, server, token, body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("submission status = %d", response.StatusCode)
	}
	decoder := json.NewDecoder(response.Body)
	accepted := nextEvent(t, decoder)
	if accepted.Type != "accepted" || accepted.LeaseID == "" {
		t.Fatalf("decision = %+v", accepted)
	}
	expectReject(t, post(t, server, token, body), 409, "already_active")
	if err := manager.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(finish)
	result := nextEvent(t, decoder)
	if result.Type != "result" || result.LeaseID != accepted.LeaseID || result.Outcome != lease.OutcomeFailed ||
		result.ExitCode == nil || *result.ExitCode != 17 || result.Inference != nil {
		t.Fatalf("result = %+v", result)
	}
	expectReject(t, post(t, server, token, body), 409, "draining")
	if err := manager.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	decoder = json.NewDecoder(post(t, server, token, body).Body)
	if event := nextEvent(t, decoder); event.Type != "accepted" {
		t.Fatalf("decision after resume = %+v", event)
	}
	if event := nextEvent(t, decoder); event.Type != "result" {
		t.Fatalf("result after resume = %+v", event)
	}
}

func TestRequesterOllamaResultIsPrivate(t *testing.T) {
	server, _, reports := newTestServer(t, func(gotDigest string, request *lease.InferenceRequest) (lease.Workload, error) {
		if gotDigest != digest || request == nil || request.Prompt != "What is a GPU?" || !request.RequireGPU {
			t.Errorf("unexpected inference launch: %q, %+v", gotDigest, request)
		}
		return &testWorkload{response: &lease.InferenceResponse{Status: "ok", Response: "generated text", Processor: "gpu"}}, nil
	}, digest)
	response := post(t, server, token, `{"inference":{"prompt":"What is a GPU?","options":{"num_predict":10}}}`)
	decoder := json.NewDecoder(response.Body)
	accepted := nextEvent(t, decoder)
	result := nextEvent(t, decoder)
	if accepted.Type != "accepted" || result.Type != "result" || result.LeaseID != accepted.LeaseID ||
		result.Outcome != lease.OutcomeCompleted || result.Inference == nil || result.Inference.Response != "generated text" {
		t.Fatalf("unexpected events: %+v, %+v", accepted, result)
	}
	if report := <-reports; report.LeaseID != result.LeaseID || report.Inference != nil {
		t.Fatalf("inference was forwarded to provider logs: %+v", report)
	}
}
