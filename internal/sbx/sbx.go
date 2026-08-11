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
	"path/filepath"
	"slices"
	"strings"

	"al.essio.dev/pkg/shellescape"

	"github.com/hawkbawk/esb/internal/docker"
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

// LoadedTemplateIdentities returns the docker.ImageIdentity of every docker
// sandbox template image loaded under templateTag's repository, deduplicated
// by image ID. There may be more than one if a previous from-template run
// skipped loading a rebuild that landed on the same layers as an
// already-loaded image.
func LoadedTemplateIdentities(templateTag string) ([]docker.ImageIdentity, error) {
	images, err := TemplateImages()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	var identities []docker.ImageIdentity
	for _, img := range images {
		// docker adds this prefix for some reason :(
		if img.Repository != templateTag || img.Repository != "docker.io/library" + templateTag {
			continue
		}
		if _, ok := seen[img.ID]; ok {
			continue
		}
		seen[img.ID] = struct{}{}

		ident, err := docker.InspectImage(img.ID)
		if err != nil {
			// docker sandbox knows about it but docker itself no longer
			// has it (e.g. pruned); nothing to compare layers against.
			continue
		}
		identities = append(identities, ident)
	}
	return identities, nil
}

// persistentEnvFile is sourced by shells inside the sandbox, so anything
// appended here survives across `sbx run`/`sbx exec` sessions.
const persistentEnvFile = "/etc/sandbox-persistent.sh"

// AddPermanentEnvVar appends `export key=value` to the sandbox's persistent
// environment file, so every later shell in that sandbox sees it.
func AddPermanentEnvVar(sandbox, key, value string) error {
	line := fmt.Sprintf("export %s=%s", key, shellescape.Quote(value))
	script := fmt.Sprintf("printf '%%s\\n' %s >> %s", shellescape.Quote(line), persistentEnvFile)

	cmd := exec.Command("sbx", "exec", "-u", "root", sandbox, "sh", "-c", script)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("setting %s in %s inside sandbox %s: %w: %s",
			key, persistentEnvFile, sandbox, err, strings.TrimSpace(errb.String()))
	}
	return nil
}

// RunScript copies the script at localPath into sandbox and executes it
// there with sh, removing the copy afterward. localPath may be relative
// (resolved against the current working directory) or absolute.
func RunScript(sandbox, localPath string) error {
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", localPath, err)
	}
	if _, err := os.Stat(absPath); err != nil {
		return fmt.Errorf("setup script %s: %w", localPath, err)
	}

	remotePath := "/tmp/" + filepath.Base(absPath)
	if err := Run("cp", absPath, sandbox+":"+remotePath); err != nil {
		return err
	}
	defer Run("exec", sandbox, "rm", "-f", remotePath)

	return Run("exec", sandbox, "sh", remotePath)
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
