// Package docker wraps the parts of the docker CLI that from-template needs
// directly, as opposed to sbx (Docker Sandbox), which wraps the sbx CLI.
package docker

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// ImageIdentity is what we know about a local docker image: the ID docker
// gave it, plus the digests of its filesystem layers.
//
// The ID alone can't tell us whether two builds produced the same image:
// buildkit stamps a fresh timestamp into the image config on every export,
// so even a fully cached rebuild gets a new ID over a byte-identical
// filesystem. The layer digests don't move, so they're what we compare.
type ImageIdentity struct {
	// ID is the short (12-hex-char) form, matching what docker sandbox
	// reports in `sbx template ls --json`.
	ID     string
	Layers []string
}

// SameContent reports whether other has the same filesystem layers as ident,
// i.e. whether loading one into docker sandbox would be equivalent to
// loading the other. An identity with no layers never matches anything,
// since that means we failed to learn what the image contains.
func (ident ImageIdentity) SameContent(other ImageIdentity) bool {
	return len(ident.Layers) > 0 && slices.Equal(ident.Layers, other.Layers)
}

// InspectImage reads the ID and layer digests docker has for ref.
func InspectImage(ref string) (ImageIdentity, error) {
	out, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}{{range .RootFS.Layers}} {{.}}{{end}}", ref).Output()
	if err != nil {
		return ImageIdentity{}, fmt.Errorf("docker image inspect %s: %w", ref, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return ImageIdentity{}, fmt.Errorf("docker image inspect %s: no output", ref)
	}
	id := strings.TrimPrefix(fields[0], "sha256:")
	if len(id) > 12 {
		id = id[:12]
	}
	return ImageIdentity{ID: id, Layers: fields[1:]}, nil
}

// SaveImage writes ref to tarPath, equivalent to `docker save -o tarPath ref`.
func SaveImage(ref, tarPath string) error {
	cmd := exec.Command("docker", "save", "-o", tarPath, ref)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
