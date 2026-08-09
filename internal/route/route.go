// Package route holds the sandbox route table: which hostname maps to which
// sandbox and published host port, and how hostnames are derived from user
// input.
package route

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Route is one hostname reachable at https://<Host>.<domain>, forwarding to
// Sandbox. A sandbox can be the target of many routes at once, which is how a
// multi-tenant app that switches on Host gets one hostname per tenant.
type Route struct {
	Host    string `json:"host"`
	Sandbox string `json:"sandbox"`
	// HostPort is bound on 127.0.0.1 by the sandbox runtime. Every route for
	// the same (Sandbox, SandboxPort) pair shares a HostPort, so that
	// container port only ever gets published once.
	HostPort int `json:"hostPort"`
	// SandboxPort is the port the app listens on inside the microVM.
	SandboxPort int `json:"sandboxPort"`
}

var (
	notLabelChar = regexp.MustCompile(`[^a-z0-9-]`)
	dashRun      = regexp.MustCompile(`-{2,}`)
)

// Sanitize flattens arbitrary input into a single DNS label. The wildcard cert
// covers *.<domain> and nothing deeper, and git worktree names routinely
// contain slashes ("feature/lti-fix"), so anything that isn't alphanumeric
// collapses to a dash. It applies equally to hostnames and sandbox labels.
func Sanitize(s string) string {
	s = strings.ToLower(s)
	s = notLabelChar.ReplaceAllString(s, "-")
	s = dashRun.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}

// Store is the daemon's persisted route table. Every mutation writes the file
// before returning, so a daemon crash can't lose a route the CLI already
// reported as created.
type Store struct {
	path string

	mu     sync.Mutex
	routes map[string]Route // keyed by Host
}

func NewStore(path string) (*Store, error) {
	s := &Store{path: path, routes: map[string]Route{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}

	var list []Route
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	for _, r := range list {
		s.routes[r.Host] = r
	}
	return s, nil
}

// List returns every route sorted by host.
func (s *Store) List() []Route {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

func (s *Store) listLocked() []Route {
	out := make([]Route, 0, len(s.routes))
	for _, r := range s.routes {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

// ForSandbox returns every route currently pointing at sandbox, sorted by
// host.
func (s *Store) ForSandbox(sandbox string) []Route {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Route, 0)
	for _, r := range s.routes {
		if r.Sandbox == sandbox {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

// Upsert records a route for host, forwarding to sandbox on sandboxPort.
//
// The host port is reused, in priority order, from: another route already
// publishing this same (sandbox, sandboxPort) pair (so a multi-tenant sandbox
// never gets the same container port published twice), or this host's own
// previous route (so a route file stays correct across sandbox restarts).
// Otherwise a fresh port is allocated.
func (s *Store) Upsert(host, sandbox string, sandboxPort, portMin, portMax int) (Route, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r := Route{Host: host, Sandbox: sandbox, SandboxPort: sandboxPort}

	if hostPort, ok := s.sharedPortLocked(sandbox, sandboxPort); ok {
		r.HostPort = hostPort
	} else if existing, ok := s.routes[host]; ok {
		r.HostPort = existing.HostPort
	} else {
		port, err := s.allocLocked(host, portMin, portMax)
		if err != nil {
			return Route{}, err
		}
		r.HostPort = port
	}

	s.routes[host] = r
	return r, s.saveLocked()
}

// sharedPortLocked reports the host port already published for sandbox's
// sandboxPort, if any other route claims it.
func (s *Store) sharedPortLocked(sandbox string, sandboxPort int) (int, bool) {
	for _, r := range s.routes {
		if r.Sandbox == sandbox && r.SandboxPort == sandboxPort {
			return r.HostPort, true
		}
	}
	return 0, false
}

// Remove drops a single host's route. It reports whether the host was
// present.
func (s *Store) Remove(host string) (Route, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.routes[host]
	if !ok {
		return Route{}, false, nil
	}
	delete(s.routes, host)
	return r, true, s.saveLocked()
}

// RemoveSandbox drops every route pointing at sandbox, e.g. when the sandbox
// itself is being destroyed. It returns the routes that were removed.
func (s *Store) RemoveSandbox(sandbox string) ([]Route, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var removed []Route
	for host, r := range s.routes {
		if r.Sandbox == sandbox {
			removed = append(removed, r)
			delete(s.routes, host)
		}
	}
	if len(removed) == 0 {
		return nil, nil
	}
	sort.Slice(removed, func(i, j int) bool { return removed[i].Host < removed[j].Host })
	return removed, s.saveLocked()
}

// allocLocked picks a deterministic starting point from the host, then probes
// upward past anything already claimed by another route or already bound on
// the host.
func (s *Store) allocLocked(host string, portMin, portMax int) (int, error) {
	taken := map[int]bool{}
	for _, r := range s.routes {
		taken[r.HostPort] = true
	}

	h := fnv.New32a()
	if _, err := h.Write([]byte(host)); err != nil {
		return 0, fmt.Errorf("hash failed: %w", err)
	}
	span := portMax - portMin + 1
	start := portMin + int(h.Sum32()%uint32(span))

	for i := start; i < portMax; i++ {
		if taken[i] || bound(i) {
			continue
		}
		return i, nil
	}
	return 0, fmt.Errorf("no free host port in %d-%d", portMin, portMax)
}

// bound reports whether something is already listening on the loopback port.
func bound(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.listLocked(), "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	// Write-then-rename so a torn write can't leave an unparseable table.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
