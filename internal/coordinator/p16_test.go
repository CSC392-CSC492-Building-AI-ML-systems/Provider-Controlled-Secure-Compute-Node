package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// request is one call the fake P16 server received.
type request struct {
	Method, Path, APIKey string
	Body                 map[string]any
}

// fakeP16 starts a server that records each request and answers with status
// and body. It returns a P16 client pointed at it.
func fakeP16(t *testing.T, status int, body string) (*P16, *[]request) {
	t.Helper()
	var got []request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := request{Method: r.Method, Path: r.URL.Path, APIKey: r.Header.Get("X-API-Key")}
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &req.Body); err != nil {
				t.Errorf("request body is not JSON: %v", err)
			}
		}
		got = append(got, req)
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return NewP16(server.URL+"/", "node-1", "secret"), &got
}

func onlyRequest(t *testing.T, got *[]request) request {
	t.Helper()
	if len(*got) != 1 {
		t.Fatalf("server got %d requests, want 1", len(*got))
	}
	return (*got)[0]
}

func TestP16RegisterSendsCapabilities(t *testing.T) {
	client, got := fakeP16(t, http.StatusCreated, `{}`)
	err := client.Register(context.Background(), Registration{
		Capabilities: Capabilities{GPUModel: "RTX 4080", VRAMMB: 16376, DriverVersion: "580"},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := onlyRequest(t, got)
	if req.Method != "POST" || req.Path != "/providers" || req.APIKey != "secret" {
		t.Fatalf("request = %s %s key=%q", req.Method, req.Path, req.APIKey)
	}
	if req.Body["provider_id"] != "node-1" {
		t.Fatalf("provider_id = %v", req.Body["provider_id"])
	}
	caps := req.Body["capabilities"].(map[string]any)
	if caps["gpu_model"] != "RTX 4080" || caps["vram_mb"] != float64(16376) {
		t.Fatalf("capabilities = %v", caps)
	}
	// Empty lists go out as [], never null, to match P16's list schema.
	if runtimes, ok := caps["runtimes"].([]any); !ok || len(runtimes) != 0 {
		t.Fatalf("runtimes = %#v, want []", caps["runtimes"])
	}
	if tiers, ok := req.Body["accepted_tiers"].([]any); !ok || len(tiers) != 0 {
		t.Fatalf("accepted_tiers = %#v, want []", req.Body["accepted_tiers"])
	}
}

func TestP16RegisterConflictMeansAlreadyRegistered(t *testing.T) {
	client, _ := fakeP16(t, http.StatusConflict, `{"error":{"code":"DUPLICATE_ID","message":"exists"}}`)
	err := client.Register(context.Background(), Registration{})
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("err = %v, want ErrAlreadyRegistered", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "DUPLICATE_ID" {
		t.Fatalf("err = %v, want the APIError kept in the chain", err)
	}
}

func TestP16HeartbeatPostsProviderTime(t *testing.T) {
	client, got := fakeP16(t, http.StatusOK, `{}`)
	if err := client.Heartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
	req := onlyRequest(t, got)
	if req.Path != "/providers/node-1/heartbeat" {
		t.Fatalf("path = %s", req.Path)
	}
	if _, ok := req.Body["provider_time"].(string); !ok || len(req.Body) != 1 {
		t.Fatalf("body = %v, want only provider_time", req.Body)
	}
}

func TestP16HeartbeatStatusErrors(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{http.StatusNotFound, ErrUnknownProvider},
		{http.StatusConflict, ErrRemoved},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			client, _ := fakeP16(t, tt.status, `{"error":{"code":"X","message":"y"}}`)
			if err := client.Heartbeat(context.Background()); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestP16UnmappedErrorKeepsRawBody(t *testing.T) {
	client, _ := fakeP16(t, http.StatusInternalServerError, `upstream exploded`)
	err := client.Heartbeat(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != 500 || apiErr.Code != "UNKNOWN" || apiErr.Message != "upstream exploded" {
		t.Fatalf("APIError = %+v", apiErr)
	}
}
