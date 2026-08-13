package proxy

import (
	"fmt"
	"strings"

	"github.com/hawkbawk/usher/internal/config"
	"github.com/hawkbawk/usher/internal/route"
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

	// A specific-address bind coexists with a wildcard bind on
	// macOS; a wildcard-vs-wildcard bind does not.
	fmt.Fprintf(&b, "\tbind %s\n\n", cfg.ListenAddress)

	// The resolvers are load-bearing. /etc/resolver/<domain> sends every lookup
	// under this domain to our own DNS server, which answers A records and
	// nothing else, so the ACME TXT propagation check would never see the
	// _acme-challenge record it just wrote. Pointing the check straight at
	// public resolvers bypasses our own DNS entirely.
	b.WriteString("\ttls {\n\t\tdns desec {\n\t\t\ttoken {env.DESEC_TOKEN}\n\t\t}\n\t\tresolvers 1.1.1.1 9.9.9.9\n\t}\n")

	for _, r := range routes {
		fmt.Fprintf(&b, "\n\t@%s host %s.%s\n", r.Host, r.Host, cfg.Domain)
		fmt.Fprintf(&b, "\thandle @%s {\n", r.Host)
		// The adapter decided the upstream. An orb route dials a
		// <machine>.orb.local name rather than an address, so that Caddy
		// re-resolves it and the route survives the VM restarting on a new IP.
		fmt.Fprintf(&b, "\t\treverse_proxy %s {\n", r.Upstream)

		b.WriteString("\t\t\theader_up X-Forwarded-Proto https\n\t\t}\n\t}\n")
	}

	// Unmatched handle, so it must come last.
	b.WriteString("\n\thandle {\n\t\trespond \"Nothing is routed at {host}. Add it with: usher up <machine> <port> <hostname>\" 404\n\t}\n")
	b.WriteString("}\n")

	return b.String()
}
