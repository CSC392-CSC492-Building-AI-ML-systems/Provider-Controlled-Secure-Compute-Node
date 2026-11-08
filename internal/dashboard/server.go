package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultDrainGrace is used when a drain request does not set grace_period_seconds.
const DefaultDrainGrace = 300 * time.Second

// Server is the dashboard HTTP API. Nil sources are allowed: their endpoints
// answer 503 until the owning group wires them in.
type Server struct {
	Status  StatusSource
	Caps    CapabilitySource
	Control Controller
	Probes  *Registry
	Events  *Bus
	// Static, if set, serves the built web app for every non-/api path.
	Static http.Handler
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/capabilities", s.handleCapabilities)
	mux.HandleFunc("GET /api/metrics", s.handleMetrics)
	mux.HandleFunc("GET /api/probes", s.handleProbes)
	mux.HandleFunc("POST /api/probes/run", s.handleRunAll)
	mux.HandleFunc("POST /api/probes/{id}/run", s.handleRunOne)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("POST /api/control/drain", s.handleDrain)
	mux.HandleFunc("POST /api/control/reclaim", s.handleReclaim)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	if s.Static != nil {
		mux.Handle("/", s.Static)
	}
	return mux
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if s.Status == nil {
		writeError(w, http.StatusServiceUnavailable, "status source not wired in")
		return
	}
	st, err := s.Status.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if s.Caps == nil {
		writeError(w, http.StatusServiceUnavailable, "capability source not wired in")
		return
	}
	c, err := s.Caps.Capabilities(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if c.GPUs == nil {
		c.GPUs = []GPU{}
	}
	if c.ProbeErrors == nil {
		c.ProbeErrors = []ProbeError{}
	}
	if c.Runtimes.CDI.Devices == nil {
		c.Runtimes.CDI.Devices = []string{}
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.Caps == nil {
		writeError(w, http.StatusServiceUnavailable, "capability source not wired in")
		return
	}
	m, err := s.Caps.Metrics(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if m.GPUs == nil {
		m.GPUs = []GPUMetrics{}
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleProbes(w http.ResponseWriter, r *http.Request) {
	if s.Probes == nil {
		writeError(w, http.StatusServiceUnavailable, "probe registry not wired in")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"probes": s.Probes.Rows()})
}

func (s *Server) handleRunAll(w http.ResponseWriter, r *http.Request) {
	if s.Probes == nil {
		writeError(w, http.StatusServiceUnavailable, "probe registry not wired in")
		return
	}
	s.log(LevelInfo, "probe", "running full audit")
	rows := s.Probes.RunAll(r.Context())
	for _, row := range rows {
		s.logProbe(row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"probes": rows})
}

func (s *Server) handleRunOne(w http.ResponseWriter, r *http.Request) {
	if s.Probes == nil {
		writeError(w, http.StatusServiceUnavailable, "probe registry not wired in")
		return
	}
	row, ok := s.Probes.Run(r.Context(), r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown probe id")
		return
	}
	s.logProbe(row)
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.Events == nil {
		writeError(w, http.StatusServiceUnavailable, "event bus not wired in")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	history, events, cancel := s.Events.Subscribe()
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	for _, e := range history {
		writeEvent(w, e)
	}
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-events:
			writeEvent(w, e)
			flusher.Flush()
		}
	}
}

func (s *Server) handleDrain(w http.ResponseWriter, r *http.Request) {
	if s.Control == nil {
		writeError(w, http.StatusServiceUnavailable, "controller not wired in")
		return
	}
	var body struct {
		GracePeriodSeconds *int `json:"grace_period_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	grace := DefaultDrainGrace
	if body.GracePeriodSeconds != nil {
		if *body.GracePeriodSeconds <= 0 {
			writeError(w, http.StatusBadRequest, "grace_period_seconds must be > 0")
			return
		}
		grace = time.Duration(*body.GracePeriodSeconds) * time.Second
	}
	if err := s.Control.Drain(r.Context(), grace); err != nil {
		s.log(LevelError, "control", "drain failed: "+err.Error())
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.log(LevelWarn, "control", fmt.Sprintf("drain requested from dashboard (grace %s)", grace))
	s.writeStatusAccepted(w, r)
}

func (s *Server) handleReclaim(w http.ResponseWriter, r *http.Request) {
	if s.Control == nil {
		writeError(w, http.StatusServiceUnavailable, "controller not wired in")
		return
	}
	if err := s.Control.Reclaim(r.Context()); err != nil {
		s.log(LevelError, "control", "reclaim failed: "+err.Error())
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.log(LevelWarn, "control", "emergency reclaim requested from dashboard")
	s.writeStatusAccepted(w, r)
}

func (s *Server) writeStatusAccepted(w http.ResponseWriter, r *http.Request) {
	if s.Status == nil {
		writeJSON(w, http.StatusAccepted, map[string]any{})
		return
	}
	st, err := s.Status.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, st)
}

func (s *Server) log(level EventLevel, source, message string) {
	if s.Events != nil {
		s.Events.Log(level, source, message)
	}
}

func (s *Server) logProbe(row ProbeRow) {
	level := LevelInfo
	switch row.Status {
	case ProbeWarn:
		level = LevelWarn
	case ProbeFail:
		level = LevelError
	}
	s.log(level, "probe", fmt.Sprintf("%s: %s - %s", row.ID, row.Status, row.Message))
}

func writeEvent(w io.Writer, e Event) {
	b, _ := json.Marshal(e)
	fmt.Fprintf(w, "data: %s\n\n", b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
