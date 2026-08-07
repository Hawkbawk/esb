// Package sbx wraps the Docker Sandbox CLI.
//
// esb never reimplements sandbox management; it only adds routing on top, so
// everything here is a thin exec of the real `sbx` binary.
package sbx

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
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

// Exists reports whether a sandbox with exactly this name is present.
func Exists(name string) (bool, error) {
	names, err := Names()
	if err != nil {
		return false, err
	}
	for _, n := range names {
		if n == name {
			return true, nil
		}
	}
	return false, nil
}
