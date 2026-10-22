// Package coordinator provides the node's coordinator communication boundary.
package coordinator

import (
	"context"
	"errors"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

// ErrNotImplemented marks the unfinished coordinator adapter.
var ErrNotImplemented = errors.New("coordinator client is not implemented")

// Client is a placeholder for coordinator communication.
type Client struct{}

// NewClient creates an unwired coordinator client.
func NewClient() *Client {
	return &Client{}
}

// ReportAccepted reports that this node accepted a lease.
func (client *Client) ReportAccepted(_ context.Context, _ string) error {
	return ErrNotImplemented
}

// ReportRejected reports that this node rejected a lease.
func (client *Client) ReportRejected(
	_ context.Context,
	_ string,
	_ lease.RejectionReason,
) error {
	return ErrNotImplemented
}

// ReportResult reports a terminal lease outcome.
func (client *Client) ReportResult(_ context.Context, _ lease.Result) error {
	return ErrNotImplemented
}
