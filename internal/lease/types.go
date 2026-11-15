// Package lease defines the node-side lease lifecycle.
package lease

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxRequestBytes = 16 << 10
	MaxPromptBytes  = 8 << 10
	MaxNumPredict   = 1024
)

// State describes the lease manager's current lifecycle state.
type State string

const (
	StateIdle     State = "idle"
	StateRunning  State = "running"
	StateDraining State = "draining"
)

// Outcome describes a terminal lease result reported to the coordinator.
type Outcome string

const (
	OutcomeCompleted Outcome = "completed"
	OutcomeFailed    Outcome = "failed"
	OutcomeExpired   Outcome = "expired"
	OutcomeReclaimed Outcome = "reclaimed"
	OutcomeRejected  Outcome = "rejected"
)

// RejectionReason explains why the node declined a lease offer.
type RejectionReason string

const (
	RejectionDigestNotAllowed RejectionReason = "digest_not_allowed"
	RejectionDraining         RejectionReason = "draining"
	RejectionAlreadyActive    RejectionReason = "already_active"
	RejectionDisconnected     RejectionReason = "disconnected"
	RejectionExpired          RejectionReason = "expired"
	RejectionInvalidOffer     RejectionReason = "invalid_offer"
)

// Offer is the coordinator's request to run a digest-pinned image.
type Offer struct {
	ID          string
	ImageDigest string
	ExpiresAt   time.Time
	Request     *InferenceRequest
}

// Active is the node's record of an accepted lease.
type Active struct {
	ID          string
	ImageDigest string
	ExpiresAt   time.Time
	Request     *InferenceRequest
}

// InferenceRequest is the provider-approved request written to a curated
// inference container's input directory. It intentionally exposes only the
// generation settings supported by the Ollama job contract.
type InferenceRequest struct {
	Prompt  string            `json:"prompt"`
	Options *InferenceOptions `json:"options,omitempty"`
	// RequireGPU is provider execution policy and is intentionally not part of
	// the request file exposed to the container.
	RequireGPU bool `json:"-"`
}

// InferenceOptions contains the bounded generation settings accepted by the
// curated inference image.
type InferenceOptions struct {
	NumPredict  *int     `json:"num_predict,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty"`
	Seed        *int     `json:"seed,omitempty"`
}

// Validate applies the request limits from the curated inference contract.
// Error messages do not include prompt contents.
func (request InferenceRequest) Validate() error {
	if !utf8.ValidString(request.Prompt) {
		return fmt.Errorf("prompt is not valid UTF-8")
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	if len(request.Prompt) > MaxPromptBytes {
		return fmt.Errorf("prompt is larger than %d bytes", MaxPromptBytes)
	}
	if strings.ContainsRune(request.Prompt, 0) {
		return fmt.Errorf("prompt contains a NUL character")
	}
	if options := request.Options; options != nil {
		if options.NumPredict != nil && (*options.NumPredict < 1 || *options.NumPredict > MaxNumPredict) {
			return fmt.Errorf("options.num_predict must be between 1 and %d", MaxNumPredict)
		}
		if options.Temperature != nil && (*options.Temperature < 0 || *options.Temperature > 2) {
			return fmt.Errorf("options.temperature must be between 0 and 2")
		}
		if options.TopP != nil && (*options.TopP <= 0 || *options.TopP > 1) {
			return fmt.Errorf("options.top_p must be greater than 0 and at most 1")
		}
		if options.TopK != nil && (*options.TopK < 1 || *options.TopK > 100) {
			return fmt.Errorf("options.top_k must be between 1 and 100")
		}
	}
	return nil
}

// Result is the terminal status sent to the coordinator.
type Result struct {
	LeaseID  string
	Outcome  Outcome
	ExitCode *int
}
