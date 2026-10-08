package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// P16 talks to Project 16's coordinator. Wire format and status-code meanings
// follow docs/coordinator-requirements.md; P16's lease endpoints exist only in
// their harness client so far, so field names there are their planned ones.
type P16 struct {
	BaseURL    string
	ProviderID string
	APIKey     string
	HTTP       *http.Client
}

func NewP16(baseURL, providerID, apiKey string) *P16 {
	return &P16{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		ProviderID: providerID,
		APIKey:     apiKey,
		HTTP:       &http.Client{Timeout: 5 * time.Second},
	}
}

func (p *P16) Register(ctx context.Context, r Registration) error {
	body := map[string]any{
		"provider_id": p.ProviderID,
		"capabilities": map[string]any{
			"gpu_model":      r.Capabilities.GPUModel,
			"vram_mb":        r.Capabilities.VRAMMB,
			"driver_version": r.Capabilities.DriverVersion,
			"runtimes":       nonNil(r.Capabilities.Runtimes),
		},
		"accepted_tiers": nonNil(r.AcceptedTiers),
	}
	return mapStatus(p.do(ctx, "POST", "/providers", body, nil), map[int]error{409: ErrAlreadyRegistered})
}

func (p *P16) Heartbeat(ctx context.Context) error {
	body := map[string]any{"provider_time": time.Now().UTC().Format(time.RFC3339Nano)}
	return mapStatus(p.do(ctx, "POST", p.providerPath("/heartbeat"), body, nil),
		map[int]error{404: ErrUnknownProvider, 409: ErrRemoved})
}

func (p *P16) Offers(ctx context.Context) ([]Offer, error) {
	var out struct {
		Leases []struct {
			LeaseID         string          `json:"lease_id"`
			JobID           string          `json:"job_id"`
			JobRequirements json.RawMessage `json:"job_requirements"`
		} `json:"leases"`
	}
	err := p.do(ctx, "GET", p.providerPath("/lease-offers"), nil, &out)
	if err = mapStatus(err, map[int]error{404: ErrUnknownProvider}); err != nil {
		return nil, err
	}
	offers := make([]Offer, len(out.Leases))
	for i, l := range out.Leases {
		offers[i] = Offer{LeaseID: l.LeaseID, JobID: l.JobID, Requirements: l.JobRequirements}
	}
	return offers, nil
}

func (p *P16) Accept(ctx context.Context, leaseID string) (time.Time, error) {
	return p.leaseExpiry(ctx, leaseID, "/accept", nil)
}

func (p *P16) Reject(ctx context.Context, leaseID string, reason RejectReason) error {
	return p.lease(ctx, leaseID, "/reject", map[string]any{"reason": reason}, nil)
}

func (p *P16) Started(ctx context.Context, leaseID string) error {
	return p.lease(ctx, leaseID, "/started", nil, nil)
}

func (p *P16) Renew(ctx context.Context, leaseID string, extend time.Duration) (time.Time, error) {
	return p.leaseExpiry(ctx, leaseID, "/renew", map[string]any{"extend_seconds": max(1, int(extend.Seconds()))})
}

func (p *P16) Report(ctx context.Context, leaseID string, r Result) error {
	body := map[string]any{"outcome": "SUCCESS"}
	if !r.Success {
		body["outcome"] = "FAILURE"
		if r.Reason != "" {
			body["reason"] = r.Reason
		}
	}
	return p.lease(ctx, leaseID, "/report", body, nil)
}

func (p *P16) Drain(ctx context.Context, grace time.Duration) error {
	body := map[string]any{"grace_period_seconds": max(1, int(grace.Seconds()))}
	return p.do(ctx, "POST", p.providerPath("/drain"), body, nil)
}

func (p *P16) providerPath(suffix string) string {
	return "/providers/" + url.PathEscape(p.ProviderID) + suffix
}

// lease calls a lease endpoint; P16 lease bodies always carry provider_id.
func (p *P16) lease(ctx context.Context, leaseID, action string, body map[string]any, out any) error {
	if body == nil {
		body = map[string]any{}
	}
	body["provider_id"] = p.ProviderID
	err := p.do(ctx, "POST", "/leases/"+url.PathEscape(leaseID)+action, body, out)
	return mapStatus(err, map[int]error{404: ErrLeaseGone, 409: ErrLeaseGone})
}

func (p *P16) leaseExpiry(ctx context.Context, leaseID, action string, body map[string]any) (time.Time, error) {
	var out struct {
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if err := p.lease(ctx, leaseID, action, body, &out); err != nil {
		return time.Time{}, err
	}
	if out.ExpiresAt == nil {
		return time.Time{}, nil
	}
	return *out.ExpiresAt, nil
}

func (p *P16) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.BaseURL+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.APIKey != "" {
		req.Header.Set("X-API-Key", p.APIKey)
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		apiErr := &APIError{Status: resp.StatusCode, Code: "UNKNOWN", Message: string(raw)}
		if json.Unmarshal(raw, &e) == nil && e.Error.Code != "" {
			apiErr.Code, apiErr.Message = e.Error.Code, e.Error.Message
		}
		return apiErr
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// mapStatus turns an APIError with a known status into a node-level error,
// keeping the original detail in the chain.
func mapStatus(err error, byStatus map[int]error) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if mapped, ok := byStatus[apiErr.Status]; ok {
			return errors.Join(mapped, err)
		}
	}
	return err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
