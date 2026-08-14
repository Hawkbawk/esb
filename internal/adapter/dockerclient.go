package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/docker/docker/client"
)

// dockerClient connects to whichever daemon the user's docker CLI would use.
//
// FromEnv is the primary path and is enough whenever DOCKER_HOST is set or the
// daemon listens on the default socket. It is not enough on its own for a
// context-based setup like OrbStack, which puts its socket under the user's
// home directory and records that only in the docker CLI's own context state,
// which the SDK never reads. So: try FromEnv, and fall back to resolving the
// active context only if that can't reach anything.
func dockerClient(ctx context.Context) (*client.Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("connecting to docker: %w", err)
	}
	if _, err := cli.Ping(ctx); err == nil {
		return cli, nil
	}

	// DOCKER_HOST was explicit, so a failure is the real answer rather than
	// something a context lookup could fix.
	if os.Getenv("DOCKER_HOST") != "" {
		return cli, nil
	}

	host, hostErr := activeContextHost()
	if hostErr != nil {
		// Report the original connection failure; the context lookup was only
		// ever a fallback and its error would be the less useful one.
		_ = cli.Close()
		return nil, fmt.Errorf("connecting to docker: %w", err)
	}

	_ = cli.Close()
	cli, err = client.NewClientWithOpts(client.WithHost(host), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("connecting to docker at %s: %w", host, err)
	}
	return cli, nil
}

// activeContextHost reads the docker endpoint of the active CLI context out of
// ~/.docker, the same state `docker context use` writes.
func activeContextHost() (string, error) {
	dir, err := dockerConfigDir()
	if err != nil {
		return "", err
	}

	name := os.Getenv("DOCKER_CONTEXT")
	if name == "" {
		var cfg struct {
			CurrentContext string `json:"currentContext"`
		}
		data, err := os.ReadFile(filepath.Join(dir, "config.json"))
		if err != nil {
			return "", err
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return "", err
		}
		name = cfg.CurrentContext
	}
	if name == "" || name == "default" {
		return "", fmt.Errorf("no docker context is active")
	}

	// Contexts are stored under the hex sha256 of their name.
	sum := sha256.Sum256([]byte(name))
	metaPath := filepath.Join(dir, "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return "", err
	}

	var meta struct {
		Endpoints struct {
			Docker struct {
				Host string `json:"Host"`
			} `json:"docker"`
		} `json:"Endpoints"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", err
	}
	if meta.Endpoints.Docker.Host == "" {
		return "", fmt.Errorf("docker context %q has no docker endpoint", name)
	}
	return meta.Endpoints.Docker.Host, nil
}

func dockerConfigDir() (string, error) {
	if dir := os.Getenv("DOCKER_CONFIG"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".docker"), nil
}
