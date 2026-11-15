package dashboard

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newMockServer(t *testing.T) *Server {
	t.Helper()
	reg := NewRegistry()
	for _, p := range MockProbes() {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	node := NewMockNode()
	return &Server{Status: node, Caps: node, Control: node, Probes: reg, Events: NewBus()}
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func TestStatus(t *testing.T) {
	rec := do(t, newMockServer(t).Handler(), "GET", "/api/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	st := decode[NodeStatus](t, rec)
	if st.State != StateIdle || st.ProviderID == "" || st.LastHeartbeatAt == nil {
		t.Fatalf("unexpected status %+v", st)
	}
}

type emptyCaps struct{}

func (emptyCaps) Capabilities(context.Context) (CapabilityReport, error) {
	return CapabilityReport{SchemaVersion: "1"}, nil
}
func (emptyCaps) Metrics(context.Context) (Metrics, error) { return Metrics{}, nil }

func TestNoGPUReportsEmptyArrays(t *testing.T) {
	h := (&Server{Caps: emptyCaps{}}).Handler()
	for _, path := range []string{"/api/capabilities", "/api/metrics"} {
		rec := do(t, h, "GET", path, "")
		if !strings.Contains(rec.Body.String(), `"gpus":[]`) {
			t.Errorf("%s: want \"gpus\":[], got %s", path, rec.Body.String())
		}
	}
	rec := do(t, h, "GET", "/api/capabilities", "")
	for _, want := range []string{`"probe_errors":[]`, `"devices":[]`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("want %s, got %s", want, rec.Body.String())
		}
	}
}

func TestUnwiredSourcesReturn503(t *testing.T) {
	h := (&Server{}).Handler()
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/status"},
		{"GET", "/api/capabilities"},
		{"GET", "/api/metrics"},
		{"GET", "/api/probes"},
		{"GET", "/api/events"},
		{"POST", "/api/control/drain"},
		{"POST", "/api/control/reclaim"},
	} {
		if rec := do(t, h, tc.method, tc.path, ""); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: status %d, want 503", tc.method, tc.path, rec.Code)
		}
	}
}

func TestUnknownAPIPathIs404(t *testing.T) {
	if rec := do(t, newMockServer(t).Handler(), "GET", "/api/nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

func TestProbeRowsBeforeRunning(t *testing.T) {
	rec := do(t, newMockServer(t).Handler(), "GET", "/api/probes", "")
	rows := decode[struct{ Probes []ProbeRow }](t, rec).Probes
	if len(rows) != len(Catalog) {
		t.Fatalf("got %d rows, want %d", len(rows), len(Catalog))
	}
	byID := map[string]ProbeRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	if s := byID["capability_discovery"].Status; s != ProbeNotRun {
		t.Errorf("registered probe: status %q, want not_run", s)
	}
	if s := byID["policy_gateway"].Status; s != ProbeNotImplemented {
		t.Errorf("unregistered probe: status %q, want not_implemented", s)
	}
}

func TestRunProbes(t *testing.T) {
	s := newMockServer(t)
	h := s.Handler()

	rec := do(t, h, "POST", "/api/probes/lan_isolation/run", "")
	row := decode[ProbeRow](t, rec)
	if rec.Code != http.StatusOK || row.Status != ProbeFail || row.CheckedAt == nil {
		t.Fatalf("run one: %d %+v", rec.Code, row)
	}
	if rec := do(t, h, "POST", "/api/probes/nope/run", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown probe: status %d, want 404", rec.Code)
	}

	rec = do(t, h, "POST", "/api/probes/run", "")
	rows := decode[struct{ Probes []ProbeRow }](t, rec).Probes
	for _, r := range rows {
		if r.Status == ProbeNotRun || r.CheckedAt == nil {
			t.Errorf("%s not run by full audit: %+v", r.ID, r)
		}
	}
	if got := do(t, h, "GET", "/api/probes", "").Body.String(); !strings.Contains(got, `"status":"warn"`) {
		t.Errorf("results not kept after audit: %s", got)
	}
}

type panicProbe struct{}

func (panicProbe) ID() string                      { return "model_mount" }
func (panicProbe) Run(context.Context) ProbeResult { panic("boom") }

func TestPanickingProbeFails(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(panicProbe{}); err != nil {
		t.Fatal(err)
	}
	row, _ := reg.Run(context.Background(), "model_mount")
	if row.Status != ProbeFail {
		t.Fatalf("status %q, want fail", row.Status)
	}
}

func TestRegisterUnknownID(t *testing.T) {
	if err := NewRegistry().Register(mockProbe{id: "not_a_probe"}); err == nil {
		t.Fatal("want error for id outside Catalog")
	}
}

func TestDrainAndReclaim(t *testing.T) {
	h := newMockServer(t).Handler()

	if rec := do(t, h, "POST", "/api/control/drain", `{"grace_period_seconds": 0}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("zero grace: status %d, want 400", rec.Code)
	}
	rec := do(t, h, "POST", "/api/control/drain", `{"grace_period_seconds": 30}`)
	if rec.Code != http.StatusAccepted || decode[NodeStatus](t, rec).State != StateDrain {
		t.Fatalf("drain: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, "POST", "/api/control/drain", ""); rec.Code != http.StatusConflict {
		t.Fatalf("second drain: status %d, want 409", rec.Code)
	}
	rec = do(t, h, "POST", "/api/control/reclaim", "")
	if rec.Code != http.StatusAccepted || decode[NodeStatus](t, rec).State != StateReclaim {
		t.Fatalf("reclaim: %d %s", rec.Code, rec.Body)
	}
}

func TestEventsStreamHistory(t *testing.T) {
	s := newMockServer(t)
	s.Events.Log(LevelWarn, "test", "hello")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}

	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var e Event
	if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), "data: ")), &e); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	if e.Source != "test" || e.Message != "hello" || e.Level != LevelWarn {
		t.Fatalf("unexpected event %+v", e)
	}
}
