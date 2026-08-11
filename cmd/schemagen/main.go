// Command schemagen renders internal/project.Config as a JSON Schema, using
// its field doc comments as the "description" keywords, and writes it to
// docs/esb.schema.json for GitHub Pages to serve.
package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/invopop/jsonschema"

	"github.com/hawkbawk/esb/internal/project"
)

func main() {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	pkgDir := filepath.Join(repoRoot, "internal", "project")

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("getting working directory: %v", err)
	}
	// AddGoComments builds its comment-map keys by joining `base` with the
	// path we give it, so the path must be relative to cwd, not absolute.
	relPkgDir, err := filepath.Rel(cwd, pkgDir)
	if err != nil {
		log.Fatalf("relativizing %s: %v", pkgDir, err)
	}

	r := &jsonschema.Reflector{
		DoNotReference: true,
	}
	if err := r.AddGoComments("github.com/hawkbawk/esb", relPkgDir); err != nil {
		log.Fatalf("reading doc comments: %v", err)
	}

	schema := r.Reflect(&project.Config{})
	schema.ID = "https://hawkbawk.github.io/esb/esb.schema.json"
	schema.Title = ".esb.json"
	schema.Description = "Per-repo config for `esb from-template`, read from .esb.json at the repo root."
	schema.Properties.Set("$schema", &jsonschema.Schema{
		Type:        "string",
		Description: "Path or URL to this JSON schema.",
	})

	out, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		log.Fatalf("marshaling schema: %v", err)
	}
	out = append(out, '\n')

	dest := filepath.Join(repoRoot, "docs", "esb.schema.json")
	if err := os.WriteFile(dest, out, 0o644); err != nil {
		log.Fatalf("writing %s: %v", dest, err)
	}
}
