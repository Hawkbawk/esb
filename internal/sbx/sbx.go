// Package sbx wraps the Docker Sandbox CLI.
//
// esb never reimplements sandbox management; it only adds routing on top, so
// everything here is a thin exec of the real `sbx` binary.
package sbx

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// Run streams a sandbox command straight to the terminal, since these are
// interactive-ish and their progress output is the point.
func Run(args ...string) error {
	cmd := exec.Command("sbx", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sbx %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// Names lists existing sandbox names from the first column of `sbx ls`.
func Names() ([]string, error) {
	cmd := exec.Command("sbx", "ls")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sbx ls: %w: %s", err, strings.TrimSpace(errb.String()))
	}

	var names []string
	scanner := bufio.NewScanner(&out)
	for i := 0; scanner.Scan(); i++ {
		if i == 0 {
			continue // header row
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) > 0 {
			names = append(names, fields[0])
		}
	}
	return names, scanner.Err()
}

// TemplateImage is one entry from `sbx template ls --json`.
type TemplateImage struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Flavor     string `json:"flavor"`
	CreatedAt  string `json:"created_at"`
	Size       int64  `json:"size"`
}

// TemplateImages lists the templates already known to docker sandbox.
func TemplateImages() ([]TemplateImage, error) {
	cmd := exec.Command("sbx", "template", "ls", "--json")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sbx template ls --json: %w: %s", err, strings.TrimSpace(errb.String()))
	}

	var parsed struct {
		Images []TemplateImage `json:"images"`
	}
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		return nil, fmt.Errorf("parsing sbx template ls --json output: %w", err)
	}
	return parsed.Images, nil
}

// shortIDLen is the length of the image IDs docker sandbox reports in
// `sbx template ls --json`. IDs are compared at this length so a full
// 64-hex-char ID still matches the short form docker sandbox prints.
const shortIDLen = 12

// LoadedTemplateImage returns the first of ids that is already loaded into
// docker sandbox as a template, or "" if none of them are. ids are given in
// preference order, and may be either the short (12-hex-char) or full form.
func LoadedTemplateImage(ids []string) (string, error) {
	images, err := TemplateImages()
	if err != nil {
		return "", err
	}

	loaded := make(map[string]struct{}, len(images))
	for _, img := range images {
		loaded[shortID(img.ID)] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := loaded[shortID(id)]; ok {
			return id, nil
		}
	}
	return "", nil
}

// shortID truncates an image ID to the form docker sandbox reports.
func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > shortIDLen {
		return id[:shortIDLen]
	}
	return id
}

// Exists reports whether a sandbox with exactly this name is present.
func Exists(name string) (bool, error) {
	names, err := Names()
	if err != nil {
		return false, err
	}
	if slices.Contains(names, name) {
		return true, nil
	}
	return false, nil
}
