// Package allowlist provides the image-digest policy boundary.
package allowlist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// ErrNotAllowed marks an image digest the provider has not approved.
var ErrNotAllowed = errors.New("image digest is not allowlisted")

// Entry approves one image digest and names the repository it is pulled from.
type Entry struct {
	Digest string `json:"digest"`
	Image  string `json:"image"`
}

// File is the on-disk allowlist format.
type File struct {
	Images []Entry `json:"images"`
}

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// Checker is the provider's digest allowlist.
type Checker struct {
	images map[string]string
}

// NewChecker builds a checker from approved entries.
func NewChecker(entries []Entry) (*Checker, error) {
	images := make(map[string]string, len(entries))
	for _, entry := range entries {
		if !digestPattern.MatchString(entry.Digest) {
			return nil, fmt.Errorf("allowlist digest %q is not sha256:<64 hex>", entry.Digest)
		}
		if entry.Image == "" || strings.ContainsAny(entry.Image, "@ \t\n") {
			return nil, fmt.Errorf("allowlist image %q for %s is not a repository name", entry.Image, entry.Digest)
		}
		if existing, found := images[entry.Digest]; found && existing != entry.Image {
			return nil, fmt.Errorf("allowlist digest %s is listed for both %q and %q", entry.Digest, existing, entry.Image)
		}
		images[entry.Digest] = entry.Image
	}
	return &Checker{images: images}, nil
}

// Load reads a JSON allowlist file, e.g. config.Config.AllowlistPath.
func Load(path string) (*Checker, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read allowlist: %w", err)
	}
	var file File
	if err := json.Unmarshal(contents, &file); err != nil {
		return nil, fmt.Errorf("parse allowlist %s: %w", path, err)
	}
	return NewChecker(file.Images)
}

// IsAllowed implements the lease image-allowlist boundary.
func (checker *Checker) IsAllowed(_ context.Context, imageDigest string) (bool, error) {
	_, found := checker.images[imageDigest]
	return found, nil
}

// ImageReference returns the pinned reference to pull, repository@digest.
// It returns ErrNotAllowed for a digest that is not allowlisted.
func (checker *Checker) ImageReference(_ context.Context, imageDigest string) (string, error) {
	image, found := checker.images[imageDigest]
	if !found {
		return "", fmt.Errorf("%w: %s", ErrNotAllowed, imageDigest)
	}
	return image + "@" + imageDigest, nil
}
