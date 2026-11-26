package jobpush

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

const maxBodyBytes = 64 << 10

// Reporter lets a live HTTP submission receive its own lease events while
// preserving the provider's existing metadata reports for every lease.
type Reporter struct {
	lease.Coordinator
	pending sync.Map // lease ID to *submission
}

type submission struct {
	accept func() error
	result chan lease.Result
}

func (reporter *Reporter) ReportAccepted(ctx context.Context, id string) error {
	if err := reporter.Coordinator.ReportAccepted(ctx, id); err != nil {
		return err
	}
	if pending, ok := reporter.pending.Load(id); ok {
		return pending.(*submission).accept()
	}
	return nil
}

func (reporter *Reporter) ReportResult(ctx context.Context, result lease.Result) error {
	if pending, ok := reporter.pending.Load(result.LeaseID); ok {
		select {
		case pending.(*submission).result <- result:
		default:
		}
		result.Inference = nil
	}
	return reporter.Coordinator.ReportResult(ctx, result)
}

// Handler submits jobs through the same manager used by provider controls.
// The Ollama digest and GPU policy are selected by the provider at startup.
func Handler(manager *lease.Manager, reporter *Reporter, token, ollamaDigest string, requireGPU bool, logger *log.Logger) http.Handler {
	auth := sha256.Sum256([]byte("Bearer " + token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", MediaType)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path != Path {
			writeEvent(w, http.StatusNotFound, Event{Type: "error", Reason: "not_found"})
			return
		}
		headers := r.Header.Values("Authorization")
		if len(headers) != 1 || !matchesAuth(headers[0], auth) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeEvent(w, http.StatusUnauthorized, Event{Type: "error", Reason: "unauthorized"})
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeEvent(w, http.StatusMethodNotAllowed, Event{Type: "error", Reason: "method_not_allowed"})
			return
		}
		contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || contentType != "application/json" {
			writeEvent(w, http.StatusUnsupportedMediaType, Event{Type: "error", Reason: "expected_json"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeEvent(w, http.StatusRequestEntityTooLarge, Event{Type: "rejected", Reason: "request_too_large"})
			return
		}
		if err != nil || !utf8.Valid(body) {
			writeEvent(w, http.StatusBadRequest, Event{Type: "rejected", Reason: "invalid_offer"})
			return
		}
		var request Submission
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || decoder.Decode(new(any)) != io.EOF {
			writeEvent(w, http.StatusBadRequest, Event{Type: "rejected", Reason: "invalid_offer"})
			return
		}
		if (request.Inference == nil && strings.TrimSpace(request.ImageDigest) == "") ||
			(request.Inference != nil && request.ImageDigest != "") ||
			request.Seconds < 0 || request.Seconds > MaxSeconds {
			writeEvent(w, http.StatusBadRequest, Event{Type: "rejected", Reason: "invalid_offer"})
			return
		}
		if request.Inference != nil {
			if _, err := request.Inference.MarshalInput(); err != nil {
				writeEvent(w, http.StatusBadRequest, Event{Type: "rejected", Reason: "invalid_inference_request"})
				return
			}
			if ollamaDigest == "" {
				writeEvent(w, http.StatusForbidden, Event{Type: "rejected", Reason: "inference_disabled"})
				return
			}
			request.ImageDigest = ollamaDigest
			request.Inference.RequireGPU = requireGPU
		}
		if request.Seconds == 0 {
			request.Seconds = DefaultSeconds
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			logger.Printf("job push: generate lease ID: %v", err)
			writeEvent(w, http.StatusInternalServerError, Event{Type: "error", Reason: "node_error"})
			return
		}
		offer := lease.Offer{
			ID:          "push-" + hex.EncodeToString(random[:]),
			ImageDigest: request.ImageDigest,
			ExpiresAt:   time.Now().Add(time.Duration(request.Seconds) * time.Second),
			Request:     request.Inference,
		}
		run(w, r, manager, reporter, offer, logger)
	})
}

func matchesAuth(header string, expected [sha256.Size]byte) bool {
	actual := sha256.Sum256([]byte(header))
	return subtle.ConstantTimeCompare(actual[:], expected[:]) == 1
}

func run(w http.ResponseWriter, r *http.Request, manager *lease.Manager, reporter *Reporter, offer lease.Offer, logger *log.Logger) {
	accepted := false
	pending := &submission{
		accept: func() error {
			accepted = true
			return writeEvent(w, http.StatusOK, Event{Type: "accepted", LeaseID: offer.ID})
		},
		result: make(chan lease.Result, 1),
	}
	reporter.pending.Store(offer.ID, pending)
	defer reporter.pending.Delete(offer.ID)

	err := manager.ReceiveOffer(r.Context(), offer)
	if err == nil {
		err = manager.WaitForOffer(r.Context(), offer.ID)
	}
	if !accepted {
		var rejected *lease.OfferRejectedError
		if errors.As(err, &rejected) {
			status := http.StatusConflict
			if rejected.Reason == lease.RejectionDigestNotAllowed {
				status = http.StatusForbidden
			}
			writeEvent(w, status, Event{Type: "rejected", LeaseID: offer.ID, Reason: string(rejected.Reason)})
			return
		}
		logger.Printf("job push %s: %v", offer.ID, err)
		writeEvent(w, http.StatusInternalServerError, Event{Type: "error", LeaseID: offer.ID, Reason: "node_error"})
		return
	}
	select {
	case result := <-pending.result:
		if err != nil && !errors.Is(err, lease.ErrNoActiveWorkload) {
			logger.Printf("job push %s: %v", offer.ID, err)
		}
		writeEvent(w, 0, Event{
			Type: "result", LeaseID: result.LeaseID, Outcome: result.Outcome,
			ExitCode: result.ExitCode, Inference: result.Inference,
		})
	default:
		logger.Printf("job push %s: result unavailable: %v", offer.ID, err)
		writeEvent(w, 0, Event{Type: "error", LeaseID: offer.ID, Reason: "result_unavailable"})
	}
}

func writeEvent(w http.ResponseWriter, status int, event Event) error {
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(WriteTimeout)); err != nil {
		return fmt.Errorf("set response write deadline: %w", err)
	}
	defer controller.SetWriteDeadline(time.Time{})
	if status != 0 {
		w.WriteHeader(status)
	}
	if err := json.NewEncoder(w).Encode(event); err != nil {
		return err
	}
	return controller.Flush()
}
