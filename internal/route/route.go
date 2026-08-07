// Package route holds the sandbox route table: which hostname label maps to
// which published host port, and how labels are derived from user input.
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

// Route is one sandbox reachable at https://<Label>.<domain>.
type Route struct {
	Label string `json:"label"`
	// HostPort is bound on 127.0.0.1 by the sandbox runtime.
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
// collapses to a dash.
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
	routes map[string]Route
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
		s.routes[r.Label] = r
	}
	return s, nil
}

// List returns every route sorted by label.
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
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// Upsert records a route for label, reusing the host port already assigned to
// that label if there is one. Reusing matters: a route file has to stay
// correct across sandbox restarts, so the port can't be re-rolled.
func (s *Store) Upsert(label string, sandboxPort, portMin, portMax int) (Route, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r := Route{Label: label, SandboxPort: sandboxPort}
	if existing, ok := s.routes[label]; ok {
		r.HostPort = existing.HostPort
	} else {
		port, err := s.allocLocked(label, portMin, portMax)
		if err != nil {
			return Route{}, err
		}
		r.HostPort = port
	}

	s.routes[label] = r
	return r, s.saveLocked()
}

// Remove drops a route. It reports whether the label was present.
func (s *Store) Remove(label string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.routes[label]; !ok {
		return false, nil
	}
	delete(s.routes, label)
	return true, s.saveLocked()
}

// allocLocked picks a deterministic starting point from the label, then probes
// upward past anything already claimed by another route or already bound on
// the host.
func (s *Store) allocLocked(label string, portMin, portMax int) (int, error) {
	taken := map[int]bool{}
	for _, r := range s.routes {
		taken[r.HostPort] = true
	}

	h := fnv.New32a()
	_, _ = h.Write([]byte(label))
	span := portMax - portMin + 1
	start := portMin + int(h.Sum32()%uint32(span))

	for i := 0; i < span; i++ {
		port := portMin + (start-portMin+i)%span
		if taken[port] || bound(port) {
			continue
		}
		return port, nil
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
