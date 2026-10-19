// Package allowlist provides the image-digest policy boundary.
package allowlist

import (
	"context"
	"errors"
)

// ErrNotImplemented marks the unfinished allowlist adapter.
var ErrNotImplemented = errors.New("allowlist checker is not implemented")

// Checker is the placeholder for the provider's digest allowlist.
type Checker struct{}

// NewChecker creates an unwired allowlist checker.
func NewChecker() *Checker {
	return &Checker{}
}

// IsAllowed implements the lease image-allowlist boundary.
func (checker *Checker) IsAllowed(_ context.Context, _ string) (bool, error) {
	return false, ErrNotImplemented
}
