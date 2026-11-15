package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Exit codes of `ollama-job verify-model`, shared with the shell scripts.
const (
	verifyOK      = 0
	verifyMissing = 10
	verifyInvalid = 20

	defaultApprovedModelConfig = "/usr/local/share/provider-node/approved-model.env"
	defaultModelRoot           = "/models"
	maxManifestBytes           = 1 << 20
	maxConfigBytes             = 64 << 10
)

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// manifestEntry is one config or layer entry of an Ollama manifest. These
// are the fields Ollama reads when it loads a model.
type manifestEntry struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	From      string `json:"from,omitempty"`
}

// approvedModel is the pinned model read from approved-model.env.
type approvedModel struct {
	Name         string
	ManifestPath string
	Config       manifestEntry
	Layers       []manifestEntry
}

// verifyError carries the exit code for a failed verification.
type verifyError struct {
	code    int
	message string
}

func (err *verifyError) Error() string { return err.message }

func notProvisionedf(format string, args ...any) error {
	return &verifyError{code: verifyMissing, message: fmt.Sprintf(format, args...)}
}

func invalidModelf(format string, args ...any) error {
	return &verifyError{code: verifyInvalid, message: fmt.Sprintf(format, args...)}
}

// verifyApprovedModel checks the model volume and returns the exit code.
func verifyApprovedModel(configPath, root string, stderr io.Writer) int {
	model, err := loadApprovedModel(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "Approved model configuration is invalid: %v\n", err)
		return verifyInvalid
	}
	if err := verifyModel(root, model); err != nil {
		fmt.Fprintln(stderr, err)
		var verifyErr *verifyError
		if errors.As(err, &verifyErr) {
			if verifyErr.code == verifyMissing {
				fmt.Fprintln(stderr, "Run containers/ollama/provision-model.sh before starting the container.")
			}
			return verifyErr.code
		}
		return verifyInvalid
	}
	return verifyOK
}

// verifyModel requires root to hold exactly the approved model: one manifest
// whose decoded entries equal the pinned ones, and every referenced blob
// with the pinned size and SHA-256 digest.
func verifyModel(root string, model approvedModel) error {
	info, err := os.Stat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return notProvisionedf("Approved model %s is not provisioned.", model.Name)
	}
	if err != nil || !info.IsDir() {
		return invalidModelf("Model directory %s is not a readable directory.", root)
	}

	manifestPath := filepath.Join(root, filepath.FromSlash(model.ManifestPath))
	if err := checkOnlyManifest(filepath.Join(root, "manifests"), manifestPath, model.Name); err != nil {
		return err
	}

	info, err = os.Lstat(manifestPath)
	if errors.Is(err, fs.ErrNotExist) {
		return notProvisionedf("Approved model %s is not provisioned.", model.Name)
	}
	if err != nil || !info.Mode().IsRegular() {
		return invalidModelf("Approved model manifest must be a regular file: %s", manifestPath)
	}
	if info.Size() > maxManifestBytes {
		return invalidModelf("Manifest for %s is larger than %d bytes.", model.Name, maxManifestBytes)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return invalidModelf("Manifest for %s could not be read: %v", model.Name, err)
	}
	if err := checkManifest(data, model); err != nil {
		return invalidModelf("Installed %s manifest is not the approved manifest: %v", model.Name, err)
	}

	for _, entry := range append([]manifestEntry{model.Config}, model.Layers...) {
		if err := checkBlob(root, entry); err != nil {
			return invalidModelf("File for %s does not match the approved manifest: %v", model.Name, err)
		}
	}
	return nil
}

// checkOnlyManifest rejects anything under manifests/ other than the
// approved manifest. Symlinks are not followed and count as entries, so a
// link cannot hide another model.
func checkOnlyManifest(manifestRoot, approvedPath, name string) error {
	var unexpected []string
	err := filepath.WalkDir(manifestRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == manifestRoot {
				return fs.SkipAll
			}
			return err
		}
		if !entry.IsDir() && path != approvedPath {
			unexpected = append(unexpected, path)
		}
		return nil
	})
	if err != nil {
		return invalidModelf("Model manifests could not be listed: %v", err)
	}
	if len(unexpected) > 0 {
		return invalidModelf("Only %s may be installed. Unapproved manifests found:\n%s", name, strings.Join(unexpected, "\n"))
	}
	return nil
}

// Keys allowed at each position of a manifest. Anything else is rejected,
// so no field can be read differently by another JSON decoder.
var (
	manifestEntryKeys = map[string]bool{"mediaType": true, "digest": true, "size": true, "from": true}
	manifestKeys      = map[string]map[string]bool{
		"":         {"schemaVersion": true, "mediaType": true, "config": true, "layers": true},
		"config":   manifestEntryKeys,
		"layers[]": manifestEntryKeys,
	}
)

// checkManifest decodes the manifest the way Ollama does (escape sequences
// included) and requires its config and layers to equal the pinned entries.
func checkManifest(data []byte, model approvedModel) error {
	if err := checkManifestKeys(data); err != nil {
		return err
	}
	var manifest struct {
		Config *manifestEntry  `json:"config"`
		Layers []manifestEntry `json:"layers"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("manifest is not valid: %v", err)
	}
	if manifest.Config == nil || *manifest.Config != model.Config {
		return errors.New("config entry differs from the pinned config")
	}
	if len(manifest.Layers) != len(model.Layers) {
		return fmt.Errorf("manifest has %d layers, want %d", len(manifest.Layers), len(model.Layers))
	}
	for i, layer := range manifest.Layers {
		if layer != model.Layers[i] {
			return fmt.Errorf("layer %d differs from the pinned layer (%s %s)", i, model.Layers[i].MediaType, model.Layers[i].Digest)
		}
	}
	return nil
}

// checkManifestKeys walks the JSON tokens and rejects unknown keys and
// duplicate keys, including duplicates that differ only in letter case,
// which decoders resolve differently.
func checkManifestKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("manifest is not valid JSON: %v", err)
	}
	if token != json.Delim('{') {
		return errors.New("manifest is not a JSON object")
	}
	if err := walkManifestObject(decoder, ""); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("manifest contains data after the JSON object")
	}
	return nil
}

// walkManifestObject checks the rest of an object whose '{' has been read.
func walkManifestObject(decoder *json.Decoder, path string) error {
	allowed, ok := manifestKeys[path]
	if !ok {
		return fmt.Errorf("unexpected object at %s", path)
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("manifest is not valid JSON: %v", err)
		}
		key := token.(string)
		if !allowed[key] {
			return fmt.Errorf("unexpected key %q", key)
		}
		folded := strings.ToLower(key)
		if seen[folded] {
			return fmt.Errorf("duplicate key %q", key)
		}
		seen[folded] = true
		child := key
		if path != "" {
			child = path + "." + key
		}
		if err := walkManifestValue(decoder, child); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("manifest is not valid JSON: %v", err)
	}
	return nil
}

func walkManifestValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("manifest is not valid JSON: %v", err)
	}
	switch token {
	case json.Delim('{'):
		return walkManifestObject(decoder, path)
	case json.Delim('['):
		for decoder.More() {
			if err := walkManifestValue(decoder, path+"[]"); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return fmt.Errorf("manifest is not valid JSON: %v", err)
		}
	}
	return nil
}

// checkBlob requires the blob for entry to be a regular file with the pinned
// size and SHA-256 digest.
func checkBlob(root string, entry manifestEntry) error {
	path := filepath.Join(root, "blobs", "sha256-"+strings.TrimPrefix(entry.Digest, "sha256:"))
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s is missing", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() != entry.Size {
		return fmt.Errorf("%s is %d bytes, want %d", path, info.Size(), entry.Size)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%s could not be read: %v", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("%s could not be read: %v", path, err)
	}
	if actual := "sha256:" + hex.EncodeToString(hash.Sum(nil)); actual != entry.Digest {
		return fmt.Errorf("%s has digest %s, want %s", path, actual, entry.Digest)
	}
	return nil
}

func loadApprovedModel(path string) (approvedModel, error) {
	file, err := os.Open(path)
	if err != nil {
		return approvedModel{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return approvedModel{}, err
	}
	if len(data) > maxConfigBytes {
		return approvedModel{}, errors.New("file is too large")
	}
	values, err := parseEnvFile(string(data))
	if err != nil {
		return approvedModel{}, err
	}
	return approvedModelFromEnv(values)
}

func approvedModelFromEnv(values map[string]string) (approvedModel, error) {
	model := approvedModel{Name: values["APPROVED_MODEL"], ManifestPath: values["APPROVED_MODEL_MANIFEST"]}
	if model.Name == "" || model.ManifestPath == "" || values["APPROVED_MANIFEST"] == "" {
		return approvedModel{}, errors.New("APPROVED_MODEL, APPROVED_MODEL_MANIFEST and APPROVED_MANIFEST are required")
	}
	clean := filepath.ToSlash(filepath.Clean(model.ManifestPath))
	if clean != model.ManifestPath || !strings.HasPrefix(clean, "manifests/") {
		return approvedModel{}, errors.New("APPROVED_MODEL_MANIFEST must be a clean path under manifests/")
	}

	haveConfig := false
	for _, line := range strings.Split(values["APPROVED_MANIFEST"], "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 4 {
			return approvedModel{}, fmt.Errorf("APPROVED_MANIFEST entry %q must have a role, media type, digest and size", line)
		}
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil || size <= 0 {
			return approvedModel{}, fmt.Errorf("APPROVED_MANIFEST entry has an invalid size %q", fields[3])
		}
		if !digestPattern.MatchString(fields[2]) {
			return approvedModel{}, fmt.Errorf("APPROVED_MANIFEST entry has an invalid digest %q", fields[2])
		}
		entry := manifestEntry{MediaType: fields[1], Digest: fields[2], Size: size}
		switch {
		case fields[0] == "config" && !haveConfig && len(model.Layers) == 0:
			model.Config, haveConfig = entry, true
		case fields[0] == "layer" && haveConfig:
			model.Layers = append(model.Layers, entry)
		default:
			return approvedModel{}, errors.New("APPROVED_MANIFEST must list one config entry followed by layer entries")
		}
	}
	if !haveConfig || len(model.Layers) == 0 {
		return approvedModel{}, errors.New("APPROVED_MANIFEST must list one config entry followed by layer entries")
	}
	return model, nil
}

var envNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// parseEnvFile reads the subset of shell syntax used by approved-model.env:
// comments, blank lines and NAME='value' assignments whose value may span
// several lines and contains no single quote.
func parseEnvFile(data string) (map[string]string, error) {
	values := map[string]string{}
	lines := strings.Split(data, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rest, ok := strings.Cut(line, "=")
		if !ok || !envNamePattern.MatchString(name) || !strings.HasPrefix(rest, "'") {
			return nil, fmt.Errorf("line %d is not a NAME='value' assignment", i+1)
		}
		if _, duplicate := values[name]; duplicate {
			return nil, fmt.Errorf("%s is assigned more than once", name)
		}
		value := rest[1:]
		for !strings.Contains(value, "'") {
			i++
			if i == len(lines) {
				return nil, fmt.Errorf("%s has no closing quote", name)
			}
			value += "\n" + lines[i]
		}
		end := strings.Index(value, "'")
		if strings.TrimSpace(value[end+1:]) != "" {
			return nil, fmt.Errorf("%s has text after its closing quote", name)
		}
		values[name] = value[:end]
	}
	return values, nil
}
