package jobworkspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

func TestNewCreatesPrivateWorkspace(t *testing.T) {
	base := t.TempDir()
	if err := os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Remove()

	for _, path := range []string{workspace.Root(), workspace.InputDir(), workspace.OutputDir()} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", path)
		}
	}
	if got := os.FileMode(mustStat(t, workspace.Root()).Mode()).Perm(); got != 0o700 {
		t.Fatalf("workspace mode = %o, want 700", got)
	}
}

func TestWriteRequestValidatesAndWritesContract(t *testing.T) {
	workspace, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Remove()

	request := lease.InferenceRequest{Prompt: "hello", Options: &lease.InferenceOptions{NumPredict: intPointer(12)}}
	if err := workspace.WriteRequest(request); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(workspace.RequestPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), `"prompt":"hello"`) {
		t.Fatalf("request = %s", contents)
	}
	if mode := mustStat(t, workspace.RequestPath()).Mode().Perm(); mode != 0o644 {
		t.Fatalf("request mode = %o, want 644", mode)
	}
}

func TestWriteRequestRejectsInvalidRequest(t *testing.T) {
	workspace, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Remove()

	for _, request := range []lease.InferenceRequest{
		{},
		{Prompt: strings.Repeat("x", lease.MaxPromptBytes+1)},
		{Prompt: "hello", Options: &lease.InferenceOptions{TopP: floatPointer(0)}},
	} {
		if err := workspace.WriteRequest(request); err == nil {
			t.Fatalf("WriteRequest(%+v) succeeded", request)
		}
	}
	if _, err := os.Stat(workspace.RequestPath()); !os.IsNotExist(err) {
		t.Fatalf("request file exists after rejected requests: %v", err)
	}
}

func TestRemoveIsIdempotent(t *testing.T) {
	workspace, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := workspace.Root()
	if err := workspace.Remove(); err != nil {
		t.Fatal(err)
	}
	if err := workspace.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists: %v", err)
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func intPointer(value int) *int           { return &value }
func floatPointer(value float64) *float64 { return &value }
