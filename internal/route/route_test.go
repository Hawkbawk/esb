package route

import (
	"path/filepath"
	"testing"
)

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"canvas-lti-fix":  "canvas-lti-fix",
		"feature/lti-fix": "feature-lti-fix",
		"Canvas_LMS":      "canvas-lms",
		"--weird--name--": "weird-name",
		"...":             "",
		// Truncation must not leave a trailing dash, which is not a legal
		// hostname label.
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/b": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

// A route file has to stay correct across sandbox restarts, so re-upserting a
// host must never re-roll its host port.
func TestUpsertReusesHostPort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routes.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	first, err := store.Upsert("canvas", "canvas", 3000, 30000, 39999)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Upsert("canvas", "canvas", 4000, 30000, 39999)
	if err != nil {
		t.Fatal(err)
	}
	if first.HostPort != second.HostPort {
		t.Errorf("host port changed on re-upsert: %d -> %d", first.HostPort, second.HostPort)
	}
	if second.SandboxPort != 4000 {
		t.Errorf("sandbox port = %d, want 4000", second.SandboxPort)
	}

	// And it has to survive a daemon restart.
	reloaded, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.List()
	if len(got) != 1 || got[0].HostPort != first.HostPort {
		t.Errorf("route table did not round-trip: %+v", got)
	}
}

func TestUpsertAvoidsCollision(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "routes.json"))
	if err != nil {
		t.Fatal(err)
	}

	seen := map[int]string{}
	for _, host := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		// A range narrow enough that the host hashes are very likely to
		// collide, so the probe-upward path actually runs.
		r, err := store.Upsert(host, host, 3000, 30000, 30015)
		if err != nil {
			t.Fatal(err)
		}
		if other, ok := seen[r.HostPort]; ok {
			t.Fatalf("port %d handed to both %q and %q", r.HostPort, other, host)
		}
		seen[r.HostPort] = host
	}
}

// A multi-tenant sandbox routes several hostnames to the same sandbox port;
// those routes must share one published host port rather than publishing the
// same container port over and over.
func TestUpsertSharesPortAcrossHostsOnSameSandboxPort(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "routes.json"))
	if err != nil {
		t.Fatal(err)
	}

	tenantA, err := store.Upsert("tenant-a", "multi-tenant", 3000, 30000, 39999)
	if err != nil {
		t.Fatal(err)
	}
	tenantB, err := store.Upsert("tenant-b", "multi-tenant", 3000, 30000, 39999)
	if err != nil {
		t.Fatal(err)
	}
	if tenantA.HostPort != tenantB.HostPort {
		t.Errorf("routes for the same (sandbox, sandboxPort) got different host ports: %d vs %d",
			tenantA.HostPort, tenantB.HostPort)
	}

	// A different sandbox port on the same sandbox still needs its own port.
	other, err := store.Upsert("tenant-a-admin", "multi-tenant", 3001, 30000, 39999)
	if err != nil {
		t.Fatal(err)
	}
	if other.HostPort == tenantA.HostPort {
		t.Errorf("routes for different sandbox ports shared a host port: %d", other.HostPort)
	}
}

func TestRemoveSandboxDropsAllItsHosts(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "routes.json"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.Upsert("tenant-a", "multi-tenant", 3000, 30000, 39999); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert("tenant-b", "multi-tenant", 3000, 30000, 39999); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert("other", "other-sandbox", 3000, 30000, 39999); err != nil {
		t.Fatal(err)
	}

	removed, err := store.RemoveSandbox("multi-tenant")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed %d routes, want 2: %+v", len(removed), removed)
	}

	remaining := store.List()
	if len(remaining) != 1 || remaining[0].Host != "other" {
		t.Errorf("route table after RemoveSandbox = %+v, want only %q", remaining, "other")
	}
}
