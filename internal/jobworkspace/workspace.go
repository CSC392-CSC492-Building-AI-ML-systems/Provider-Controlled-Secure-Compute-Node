// Package jobworkspace owns the provider-side files exchanged with a
// curated inference container.
package jobworkspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

const (
	inputDirectoryMode  = 0o755
	outputDirectoryMode = 0o777
	requestFileMode     = 0o644
)

// Workspace is one provider-owned input/output directory pair. Its parent is
// private to the provider, so the output directory can be writable by the
// container's unprivileged UID without exposing it to other local users.
type Workspace struct {
	root       string
	inputDir   string
	outputDir  string
	removeOnce sync.Once
	removeErr  error
}

// New creates a workspace below baseDir. An empty baseDir uses the host's
// temporary directory. The workspace itself is owner-only; callers should
// provide a provider-private base directory when one is available.
func New(baseDir string) (*Workspace, error) {
	if baseDir == "" {
		baseDir = os.TempDir()
	}
	info, err := os.Stat(baseDir)
	if err != nil {
		return nil, fmt.Errorf("inspect workspace parent: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace parent %q is not a directory", baseDir)
	}

	root, err := os.MkdirTemp(baseDir, "compute-node-job-")
	if err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	inputDir := filepath.Join(root, "input")
	outputDir := filepath.Join(root, "output")
	if err := os.Mkdir(inputDir, inputDirectoryMode); err != nil {
		cleanup()
		return nil, fmt.Errorf("create workspace input directory: %w", err)
	}
	if err := os.Chmod(inputDir, inputDirectoryMode); err != nil {
		cleanup()
		return nil, fmt.Errorf("restrict workspace input directory: %w", err)
	}
	if err := os.Mkdir(outputDir, outputDirectoryMode); err != nil {
		cleanup()
		return nil, fmt.Errorf("create workspace output directory: %w", err)
	}
	if err := os.Chmod(outputDir, outputDirectoryMode); err != nil {
		cleanup()
		return nil, fmt.Errorf("set workspace output directory permissions: %w", err)
	}
	return &Workspace{root: root, inputDir: inputDir, outputDir: outputDir}, nil
}

func (workspace *Workspace) Root() string      { return workspace.root }
func (workspace *Workspace) InputDir() string  { return workspace.inputDir }
func (workspace *Workspace) OutputDir() string { return workspace.outputDir }
func (workspace *Workspace) RequestPath() string {
	return filepath.Join(workspace.inputDir, "request.json")
}
func (workspace *Workspace) ResponsePath() string {
	return filepath.Join(workspace.outputDir, "response.json")
}

// WriteRequest validates and writes exactly one bounded JSON request.
func (workspace *Workspace) WriteRequest(request lease.InferenceRequest) error {
	if workspace == nil || workspace.root == "" {
		return errors.New("workspace is not initialized")
	}
	contents, err := request.MarshalInput()
	if err != nil {
		return fmt.Errorf("prepare inference request: %w", err)
	}
	if err := os.WriteFile(workspace.RequestPath(), contents, requestFileMode); err != nil {
		return fmt.Errorf("write inference request: %w", err)
	}
	if err := os.Chmod(workspace.RequestPath(), requestFileMode); err != nil {
		return fmt.Errorf("set inference request permissions: %w", err)
	}
	return nil
}

// Remove deletes the complete workspace. It is safe to call repeatedly.
func (workspace *Workspace) Remove() error {
	if workspace == nil {
		return nil
	}
	workspace.removeOnce.Do(func() {
		workspace.removeErr = os.RemoveAll(workspace.root)
	})
	return workspace.removeErr
}
