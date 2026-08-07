package dnsd

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func start(t *testing.T) int {
	t.Helper()

	s, err := New("sbx.example.dedyn.io", "192.168.255.253")
	if err != nil {
		t.Fatal(err)
	}

	// Ask the kernel for a free port, then hand it to the server.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	_ = pc.Close()

	if err := s.Start(port, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return port
}

func query(t *testing.T, port int, name string, qtype uint16) *dns.Msg {
	t.Helper()

	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)

	c := &dns.Client{Timeout: 2 * time.Second}
	resp, _, err := c.Exchange(m, fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("query %s: %v", name, err)
	}
	return resp
}

// Any label under the domain resolves to the loopback alias, including ones
// no sandbox has claimed. Caddy's fallback handler answers those with a 404,
// which is a much better error than a DNS failure.
func TestAnswersWildcardA(t *testing.T) {
	port := start(t)

	for _, name := range []string{"canvas-lti-fix.sbx.example.dedyn.io", "anything.sbx.example.dedyn.io"} {
		resp := query(t, port, name, dns.TypeA)
		if resp.Rcode != dns.RcodeSuccess {
			t.Fatalf("%s: rcode = %s, want NOERROR", name, dns.RcodeToString[resp.Rcode])
		}
		if len(resp.Answer) != 1 {
			t.Fatalf("%s: got %d answers, want 1", name, len(resp.Answer))
		}
		a, ok := resp.Answer[0].(*dns.A)
		if !ok || a.A.String() != "192.168.255.253" {
			t.Fatalf("%s: answer = %v, want 192.168.255.253", name, resp.Answer[0])
		}
	}
}

// Empty NOERROR, not NXDOMAIN: a client asking for AAAA has to fall through to
// the A record rather than conclude the name does not exist.
func TestAAAAIsEmptyNoError(t *testing.T) {
	port := start(t)

	resp := query(t, port, "canvas.sbx.example.dedyn.io", dns.TypeAAAA)
	if resp.Rcode != dns.RcodeSuccess {
		t.Errorf("rcode = %s, want NOERROR", dns.RcodeToString[resp.Rcode])
	}
	if len(resp.Answer) != 0 {
		t.Errorf("got %d answers, want 0", len(resp.Answer))
	}
}

// This must never become a general-purpose resolver.
func TestRefusesOtherDomains(t *testing.T) {
	port := start(t)

	for _, name := range []string{"example.com", "sbx.example.dedyn.io.evil.com", "notsbx.example.dedyn.io"} {
		resp := query(t, port, name, dns.TypeA)
		if resp.Rcode != dns.RcodeRefused {
			t.Errorf("%s: rcode = %s, want REFUSED", name, dns.RcodeToString[resp.Rcode])
		}
	}
}
