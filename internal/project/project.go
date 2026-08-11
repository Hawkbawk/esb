// Package project reads the per-repo bits `esb from-template` needs: the
// optional esb.json next to the Dockerfile, and the git commit the image tag
// gets pinned to.
package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
)

// ConfigName is the per-repo config file, expected inside the sandbox
// directory alongside the Dockerfile.
const ConfigName = "esb.json"

//go:generate go run ../../cmd/schemagen

// Config is the schema of esb.json. Its JSON Schema, for editor autocomplete,
// is generated from this struct (including these doc comments) by
// cmd/schemagen into docs/esb.schema.json, published via GitHub Pages at
// https://hawkbawk.github.io/esb/esb.schema.json. Run `go generate ./...`
// after changing this struct to regenerate it.
type Config struct {
	// Kits are passed straight through to `sbx create --kit`, so each entry
	// is whatever that flag accepts: a local path, a URL, or an OCI
	// reference.
	Kits []string `json:"kits,omitempty"`

	// Dockerfile overrides the Dockerfile used to build the template image.
	// It's either a path relative to the repo root (the CWD `from-template`
	// is expected to be run from) or an absolute path. If empty, the
	// Dockerfile directly inside the sandbox directory is used instead.
	Dockerfile string `json:"dockerfile,omitempty"`

	// SetupScript is a shell script, either relative to the repo root (the
	// CWD `from-template` is expected to be run from) or an absolute path,
	// that's copied into the sandbox and run there once everything else in
	// from-template has finished. If empty, no setup script is run.
	SetupScript string `json:"setupScript,omitempty"`
}

// LoadConfig reads dir/esb.json. The file is optional, so a missing one gives
// back a zero Config rather than an error.
func LoadConfig(dir string) (*Config, error) {
	path := filepath.Join(dir, ConfigName)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}
	defer f.Close()

	var c Config
	if err := json.NewDecoder(f).Decode(&c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &c, nil
}

// GitShortSHA is the abbreviated HEAD of the repo containing dir.
func GitShortSHA(dir string) (string, error) {
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return "", err
	}
	head, err := repo.Head()
	if err != nil {
		return "", err
	}
	hash := head.Hash().String()
	return hash[:7], nil
}
