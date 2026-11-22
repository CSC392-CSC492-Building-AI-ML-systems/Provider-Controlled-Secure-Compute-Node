package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
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
		req := request{Method: r.Method, Path: r.URL.EscapedPath(), APIKey: r.Header.Get("X-API-Key")}
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
	client, got := fakeP16(t, http.StatusOK, `{"status":"ACTIVE"}`)
	if status, err := client.Heartbeat(context.Background()); err != nil || status != "ACTIVE" {
		t.Fatalf("heartbeat status = %q, err = %v", status, err)
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
			if _, err := client.Heartbeat(context.Background()); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestP16UnmappedErrorKeepsRawBody(t *testing.T) {
	client, _ := fakeP16(t, http.StatusInternalServerError, `upstream exploded`)
	_, err := client.Heartbeat(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != 500 || apiErr.Code != "UNKNOWN" || apiErr.Message != "upstream exploded" {
		t.Fatalf("APIError = %+v", apiErr)
	}
}

func TestP16AcceptReturnsExpiry(t *testing.T) {
	client, got := fakeP16(t, http.StatusOK, `{"lease_status":"ACTIVE","expires_at":"2026-11-21T12:00:00Z"}`)
	expires, err := client.Accept(context.Background(), "lease/1")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC); !expires.Equal(want) {
		t.Fatalf("expires = %v, want %v", expires, want)
	}
	req := onlyRequest(t, got)
	if req.Path != "/leases/lease%2F1/accept" || req.Body["provider_id"] != "node-1" {
		t.Fatalf("request = %s %v", req.Path, req.Body)
	}
}

func TestP16AcceptWithoutExpiryReturnsZero(t *testing.T) {
	client, _ := fakeP16(t, http.StatusOK, `{"lease_status":"ACTIVE"}`)
	expires, err := client.Accept(context.Background(), "lease-1")
	if err != nil || !expires.IsZero() {
		t.Fatalf("expires, err = %v, %v; want zero, nil", expires, err)
	}
}

func TestP16RejectSendsReason(t *testing.T) {
	client, got := fakeP16(t, http.StatusOK, `{}`)
	if err := client.Reject(context.Background(), "lease-1", RejectLocalBusy); err != nil {
		t.Fatal(err)
	}
	req := onlyRequest(t, got)
	if req.Path != "/leases/lease-1/reject" || req.Body["reason"] != "LOCAL_BUSY" || req.Body["provider_id"] != "node-1" {
		t.Fatalf("request = %s %v", req.Path, req.Body)
	}
}

func TestP16ReportBody(t *testing.T) {
	tests := []struct {
		name   string
		result Result
		want   map[string]any
	}{
		{"success", Result{Success: true}, map[string]any{"provider_id": "node-1", "outcome": "SUCCESS"}},
		{"failure", Result{Reason: FailExecution}, map[string]any{"provider_id": "node-1", "outcome": "FAILURE", "reason": "EXECUTION_ERROR"}},
		{"failure without reason", Result{}, map[string]any{"provider_id": "node-1", "outcome": "FAILURE"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, got := fakeP16(t, http.StatusOK, `{}`)
			if err := client.Report(context.Background(), "lease-1", tt.result); err != nil {
				t.Fatal(err)
			}
			req := onlyRequest(t, got)
			if req.Path != "/leases/lease-1/report" || !maps.Equal(req.Body, tt.want) {
				t.Fatalf("request = %s %v, want body %v", req.Path, req.Body, tt.want)
			}
		})
	}
}

func TestP16LeaseCallsTreatNotFoundAndConflictAsGone(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client, _ := fakeP16(t, status, `{"error":{"code":"X","message":"y"}}`)
			ctx := context.Background()
			if _, err := client.Accept(ctx, "lease-1"); !errors.Is(err, ErrLeaseGone) {
				t.Errorf("Accept err = %v, want ErrLeaseGone", err)
			}
			if err := client.Reject(ctx, "lease-1", RejectPolicy); !errors.Is(err, ErrLeaseGone) {
				t.Errorf("Reject err = %v, want ErrLeaseGone", err)
			}
			if err := client.Report(ctx, "lease-1", Result{Success: true}); !errors.Is(err, ErrLeaseGone) {
				t.Errorf("Report err = %v, want ErrLeaseGone", err)
			}
		})
	}
}

func TestP16RegisterSendsOnlyDocumentedFields(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/providers" || r.Header.Get("X-API-Key") != "k" {
			t.Errorf("%s %s key=%q", r.Method, r.URL.Path, r.Header.Get("X-API-Key"))
		}
		json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(201)
	}))
	defer srv.Close()
	err := NewP16(srv.URL, "n1", "k").Register(context.Background(), Registration{
		Capabilities:  Capabilities{GPUModel: "RTX", VRAMMB: 8, DriverVersion: "1", Runtimes: []string{"podman"}},
		AcceptedTiers: []string{"standard"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"provider_id": "n1",
		"capabilities": map[string]any{
			"gpu_model": "RTX", "vram_mb": float64(8), "driver_version": "1", "runtimes": []any{"podman"},
		},
		"accepted_tiers": []any{"standard"},
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %v", body)
	}
}

func TestP16HeartbeatStatus(t *testing.T) {
	for _, tc := range []struct {
		body string
		want ProviderStatus
	}{
		{`{"status":"ACTIVE"}`, "ACTIVE"},
		{`{"status":"STALE"}`, "STALE"},
		{`{"status":"DRAINING"}`, "DRAINING"},
		{`{"status":"DRAINED"}`, ProviderDrained},
		{`{"status":"UNKNOWN"}`, ""},
		{`{}`, ""},
	} {
		t.Run(tc.body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			status, err := NewP16(srv.URL, "n1", "").Heartbeat(context.Background())
			if status != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("heartbeat status = %q, error = %v, want %q", status, err, tc.want)
			}
		})
	}
}
