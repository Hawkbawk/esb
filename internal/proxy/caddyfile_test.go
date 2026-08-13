package proxy

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig"

	"github.com/hawkbawk/usher/internal/config"
	"github.com/hawkbawk/usher/internal/route"
)

func testConfig() *config.Config {
	return &config.Config{
		Domain:        "sbx.example.dedyn.io",
		ListenAddress: net.IPv4(192, 168, 255, 253),
		StateDir:      "/tmp/usher-test",
		ACMEEmail:     "me@example.com",
	}
}

// The whole point of compiling Caddy into this binary is that `dns desec`
// resolves to a real module. If the plugin import ever gets dropped, adapting
// fails here rather than at 3am when a cert needs renewing.
//
// One route per adapter, because an orb upstream is a hostname rather than an
// address and Caddy has to accept both.
func TestCaddyfileAdapts(t *testing.T) {
	routes := []route.Route{
		{Host: "canvas-lti-fix", Adapter: route.Sbx, Machine: "canvas-lti-fix",
			MachinePort: 3000, HostPort: 31847, Upstream: "127.0.0.1:31847"},
		{Host: "tool", Adapter: route.Docker, Machine: "tool",
			MachinePort: 8080, HostPort: 30001, Upstream: "127.0.0.1:30001"},
		{Host: "canvas", Adapter: route.Orb, Machine: "canvas-lms",
			MachinePort: 3000, Upstream: "canvas-lms.orb.local:3000"},
		{Host: "legacy", Adapter: route.Static, Machine: "10.0.0.5",
			MachinePort: 8080, Upstream: "10.0.0.5:8080"},
	}
	src := Caddyfile(testConfig(), routes)

	adapter := caddyconfig.GetAdapter("caddyfile")
	if adapter == nil {
		t.Fatal("caddyfile adapter is not registered")
	}

	cfgJSON, warnings, err := adapter.Adapt([]byte(src), map[string]any{"filename": "usher.caddyfile"})
	if err != nil {
		t.Fatalf("adapting caddyfile: %v\n\n%s", err, src)
	}
	for _, w := range warnings {
		t.Errorf("caddy warning: line %d: %s", w.Line, w.Message)
	}

	var parsed map[string]any
	if err := json.Unmarshal(cfgJSON, &parsed); err != nil {
		t.Fatalf("adapted config is not valid JSON: %v", err)
	}
	want := []string{
		"127.0.0.1:31847",
		"127.0.0.1:30001",
		"canvas-lms.orb.local:3000",
		"10.0.0.5:8080",
		"canvas-lti-fix.sbx.example.dedyn.io",
	}
	for _, w := range want {
		if !strings.Contains(string(cfgJSON), w) {
			t.Errorf("adapted config is missing %q", w)
		}
	}
}

// An empty route table still has to produce a loadable config: that is the
// state the daemon boots into on a fresh machine.
func TestCaddyfileAdaptsWithNoRoutes(t *testing.T) {
	src := Caddyfile(testConfig(), nil)
	adapter := caddyconfig.GetAdapter("caddyfile")
	if _, _, err := adapter.Adapt([]byte(src), map[string]any{"filename": "usher.caddyfile"}); err != nil {
		t.Fatalf("adapting empty caddyfile: %v\n\n%s", err, src)
	}
}
