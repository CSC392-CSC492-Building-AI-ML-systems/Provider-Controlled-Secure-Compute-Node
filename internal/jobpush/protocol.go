// Package jobpush implements the node's direct requester job protocol.
package jobpush

import (
	"fmt"
	"net"
	"regexp"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

const (
	Path           = "/v1/jobs"
	TokenEnv       = "NODE_JOB_TOKEN"
	MediaType      = "application/x-ndjson"
	DefaultSeconds = 30 * 60
	MaxSeconds     = 60 * 60
	WriteTimeout   = 5 * time.Second
)

var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{32,}$`)

func ValidateToken(token string) error {
	if !tokenPattern.MatchString(token) {
		return fmt.Errorf("%s must contain at least 32 letters, digits, underscores or hyphens", TokenEnv)
	}
	return nil
}

// IsLoopbackIP accepts literal loopback addresses without DNS resolution.
func IsLoopbackIP(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Submission chooses one generic approved image or submits to the configured
// inference image. The requesting client cannot choose container arguments.
type Submission struct {
	ImageDigest string                  `json:"image_digest,omitempty"`
	Inference   *lease.InferenceRequest `json:"inference,omitempty"`
	Seconds     int                     `json:"seconds,omitempty"`
}

// Event is one line of the HTTP response. An accepted event is followed by a
// result. Rejections and HTTP errors have a single event.
type Event struct {
	Type      string                   `json:"type"`
	LeaseID   string                   `json:"lease_id,omitempty"`
	Outcome   lease.Outcome            `json:"outcome,omitempty"`
	ExitCode  *int                     `json:"exit_code,omitempty"`
	Inference *lease.InferenceResponse `json:"inference,omitempty"`
	Reason    string                   `json:"reason,omitempty"`
}
