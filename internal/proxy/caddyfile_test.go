package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig"

	"github.com/rhawk/esb/internal/config"
	"github.com/rhawk/esb/internal/route"
)

func testConfig() *config.Config {
	return &config.Config{
		Domain:        "sbx.example.dedyn.io",
		ListenAddress: "192.168.255.253",
		StateDir:      "/tmp/esb-test",
		ACMEEmail:     "me@example.com",
	}
}

// The whole point of compiling Caddy into this binary is that `dns desec`
// resolves to a real module. If the plugin import ever gets dropped, adapting
// fails here rather than at 3am when a cert needs renewing.
func TestCaddyfileAdapts(t *testing.T) {
	routes := []route.Route{
		{Label: "canvas-lti-fix", HostPort: 31847, SandboxPort: 3000},
		{Label: "tool", HostPort: 30001, SandboxPort: 8080},
	}
	src := Caddyfile(testConfig(), routes)

	adapter := caddyconfig.GetAdapter("caddyfile")
	if adapter == nil {
		t.Fatal("caddyfile adapter is not registered")
	}

	cfgJSON, warnings, err := adapter.Adapt([]byte(src), map[string]any{"filename": "esb.caddyfile"})
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
	for _, want := range []string{"127.0.0.1:31847", "127.0.0.1:30001", "canvas-lti-fix.sbx.example.dedyn.io"} {
		if !strings.Contains(string(cfgJSON), want) {
			t.Errorf("adapted config is missing %q", want)
		}
	}
}

// An empty route table still has to produce a loadable config: that is the
// state the daemon boots into on a fresh machine.
func TestCaddyfileAdaptsWithNoRoutes(t *testing.T) {
	src := Caddyfile(testConfig(), nil)
	adapter := caddyconfig.GetAdapter("caddyfile")
	if _, _, err := adapter.Adapt([]byte(src), map[string]any{"filename": "esb.caddyfile"}); err != nil {
		t.Fatalf("adapting empty caddyfile: %v\n\n%s", err, src)
	}
}
