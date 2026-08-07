package proxy

import (
	"fmt"
	"strings"

	"github.com/hawkbawk/esb/internal/config"
	"github.com/hawkbawk/esb/internal/route"
)

// Caddyfile renders the whole proxy config from the current route table.
//
// The daemon adapts and loads this in-process, which is why `admin off`: there
// is no admin API for anyone to post to, and no second copy of Caddy that
// would need the desec plugin compiled in just to adapt a config client-side.
func Caddyfile(cfg *config.Config, routes []route.Route) string {
	var b strings.Builder

	fmt.Fprintf(&b, "{\n\tadmin off\n\tstorage file_system %s\n", cfg.CaddyStorageDir())
	if cfg.ACMEEmail != "" {
		fmt.Fprintf(&b, "\temail %s\n", cfg.ACMEEmail)
	}
	b.WriteString("}\n\n")

	fmt.Fprintf(&b, "*.%s {\n", cfg.Domain)

	// Bind the dedicated alias only. Without this Caddy listens on 0.0.0.0:443
	// and 0.0.0.0:80, which collides with OrbStack's existing wildcard binds on
	// both ports. A specific-address bind coexists with a wildcard bind on
	// macOS; a wildcard-vs-wildcard bind does not.
	fmt.Fprintf(&b, "\tbind %s\n\n", cfg.ListenAddress)

	// The desec provider takes a block with a `token` subdirective; the shorter
	// `dns desec <token>` form does not parse.
	//
	// The resolvers are load-bearing. /etc/resolver/<domain> sends every lookup
	// under this domain to our own DNS server, which answers A records and
	// nothing else, so the ACME TXT propagation check would never see the
	// _acme-challenge record it just wrote. Pointing the check straight at
	// public resolvers bypasses our own DNS entirely.
	b.WriteString("\ttls {\n\t\tdns desec {\n\t\t\ttoken {env.DESEC_TOKEN}\n\t\t}\n\t\tresolvers 1.1.1.1 9.9.9.9\n\t}\n")

	for _, r := range routes {
		fmt.Fprintf(&b, "\n\t@%s host %s.%s\n", r.Label, r.Label, cfg.Domain)
		fmt.Fprintf(&b, "\thandle @%s {\n", r.Label)
		fmt.Fprintf(&b, "\t\treverse_proxy 127.0.0.1:%d {\n", r.HostPort)

		// Canvas decides it is on HTTPS from this header. TLS terminates here
		// at the proxy; the hop into the sandbox is plaintext, which is fine
		// because LTI signing covers the URL and body, not the transport.
		b.WriteString("\t\t\theader_up X-Forwarded-Proto https\n\t\t}\n\t}\n")
	}

	// Unmatched handle, so it must come last.
	b.WriteString("\n\thandle {\n\t\trespond \"No sandbox is routed at {host}. Create one with: esb up <label> <sandbox-port> <workspace>\" 404\n\t}\n")
	b.WriteString("}\n")

	return b.String()
}
