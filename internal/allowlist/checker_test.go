package allowlist

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const (
	ollamaDigest = "sha256:265409e1d931028a8f7d5a32bcbafddd7bffbc6cc629d77efe9560adf692a5e7"
	otherDigest  = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func TestCheckerAllowsOnlyListedDigests(t *testing.T) {
	checker, err := NewChecker([]Entry{{Digest: ollamaDigest, Image: "docker.io/ollama/ollama"}})
	if err != nil {
		t.Fatalf("NewChecker() returned unexpected error: %v", err)
	}

	allowed, err := checker.IsAllowed(context.Background(), ollamaDigest)
	if err != nil || !allowed {
		t.Errorf("IsAllowed(listed) = %v, %v, want true, nil", allowed, err)
	}
	allowed, err = checker.IsAllowed(context.Background(), otherDigest)
	if err != nil || allowed {
		t.Errorf("IsAllowed(unlisted) = %v, %v, want false, nil", allowed, err)
	}

	reference, err := checker.ImageReference(context.Background(), ollamaDigest)
	if err != nil || reference != "docker.io/ollama/ollama@"+ollamaDigest {
		t.Errorf("ImageReference(listed) = %q, %v", reference, err)
	}
	if _, err := checker.ImageReference(context.Background(), otherDigest); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("ImageReference(unlisted) error = %v, want ErrNotAllowed", err)
	}
}

func TestNewCheckerRejectsBadEntries(t *testing.T) {
	tests := map[string][]Entry{
		"short digest":      {{Digest: "sha256:abc", Image: "docker.io/ollama/ollama"}},
		"no algorithm":      {{Digest: ollamaDigest[len("sha256:"):], Image: "docker.io/ollama/ollama"}},
		"missing image":     {{Digest: ollamaDigest}},
		"image with digest": {{Digest: ollamaDigest, Image: "docker.io/ollama/ollama@" + otherDigest}},
		"conflicting images": {
			{Digest: ollamaDigest, Image: "docker.io/ollama/ollama"},
			{Digest: ollamaDigest, Image: "docker.io/attacker/ollama"},
		},
	}
	for name, entries := range tests {
		if _, err := NewChecker(entries); err == nil {
			t.Errorf("%s: NewChecker() returned nil error", name)
		}
	}
}

func TestLoadReadsJSONFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowlist.json")
	contents := `{"images": [{"digest": "` + ollamaDigest + `", "image": "docker.io/ollama/ollama"}]}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	checker, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}
	if allowed, _ := checker.IsAllowed(context.Background(), ollamaDigest); !allowed {
		t.Error("digest from file is not allowed")
	}

	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("Load(missing file) returned nil error")
	}
}
