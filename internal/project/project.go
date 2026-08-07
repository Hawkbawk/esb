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

// Config is the schema of esb.json.
type Config struct {
	// Kits are passed straight through to `sbx create --kit`, so each entry
	// is whatever that flag accepts: a local path, a URL, or an OCI
	// reference.
	Kits []string `json:"kits"`
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
