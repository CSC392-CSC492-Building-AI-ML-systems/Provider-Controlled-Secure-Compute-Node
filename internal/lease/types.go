// Package lease defines the node-side lease lifecycle.
package lease

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxRequestBytes      = 16 << 10
	MaxPromptBytes       = 8 << 10
	MaxNumPredict        = 1024
	MaxResponseFileBytes = 128 << 10
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

// MarshalInput validates and encodes one complete Ollama request file.
func (request InferenceRequest) MarshalInput() ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	contents, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode inference request: %w", err)
	}
	contents = append(contents, '\n')
	if len(contents) > MaxRequestBytes {
		return nil, fmt.Errorf("inference request is larger than %d bytes", MaxRequestBytes)
	}
	return contents, nil
}

// Result is the terminal status sent to the coordinator.
type Result struct {
	LeaseID   string
	Outcome   Outcome
	ExitCode  *int
	Inference *InferenceResponse
}

// InferenceResponse is the structured result produced by the curated Ollama
// job runner.
type InferenceResponse struct {
	Status     string           `json:"status"`
	Model      string           `json:"model"`
	Response   string           `json:"response,omitempty"`
	DoneReason string           `json:"done_reason,omitempty"`
	Truncated  bool             `json:"truncated,omitempty"`
	Processor  string           `json:"processor,omitempty"`
	Metrics    InferenceMetrics `json:"metrics,omitempty"`
	Error      *InferenceError  `json:"error,omitempty"`
}

type InferenceMetrics struct {
	PromptTokens     int   `json:"prompt_tokens,omitempty"`
	CompletionTokens int   `json:"completion_tokens,omitempty"`
	TotalDurationMS  int64 `json:"total_duration_ms,omitempty"`
}

type InferenceError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Validate checks the response shape without including generated text in
// validation errors.
func (response InferenceResponse) Validate() error {
	switch response.Status {
	case "ok":
		if strings.TrimSpace(response.Response) == "" {
			return fmt.Errorf("successful inference response is empty")
		}
		if response.Error != nil {
			return fmt.Errorf("successful inference response contains an error")
		}
		if response.Processor != "cpu" && response.Processor != "gpu" &&
			response.Processor != "partial" && response.Processor != "unknown" {
			return fmt.Errorf("inference response has invalid processor")
		}
	case "error":
		if response.Error == nil || strings.TrimSpace(response.Error.Code) == "" ||
			strings.TrimSpace(response.Error.Message) == "" {
			return fmt.Errorf("error inference response is incomplete")
		}
	default:
		return fmt.Errorf("inference response has invalid status")
	}
	return nil
}
