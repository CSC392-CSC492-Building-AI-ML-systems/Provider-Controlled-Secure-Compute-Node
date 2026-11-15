// Package config defines node-agent configuration.
package config

import "errors"

// ErrNotImplemented marks the unfinished configuration loader.
var ErrNotImplemented = errors.New("configuration loading is not implemented")

// Config contains the external settings needed to wire the node agent.
type Config struct {
	CoordinatorURL string
	AllowlistPath  string
	ContainerTool  string
}

// Load is reserved for configuration loading and validation.
func Load() (Config, error) {
	return Config{}, ErrNotImplemented
}
