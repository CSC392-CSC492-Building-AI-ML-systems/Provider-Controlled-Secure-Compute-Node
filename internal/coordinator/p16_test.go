package coordinator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

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
