// Package dnsd is the dnsmasq replacement.
//
// It answers exactly one question: "what is the address of anything under
// <domain>?" Everything else gets REFUSED. It never consults /etc/resolv.conf
// or /etc/hosts, so it can't accidentally become a general-purpose resolver.
package dnsd

import (
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/miekg/dns"
)

type Server struct {
	// domain is stored fully qualified and lower-cased, e.g. "sbx.example.io."
	domain string
	ip     net.IP

	mu      sync.Mutex
	servers []*dns.Server
}

func New(domain string, listenIP net.IP) (*Server, error) {
	if listenIP == nil || listenIP.To4() == nil {
		return nil, fmt.Errorf("listen address %q is not an IPv4 address", listenIP)
	}
	return &Server{
		domain: dns.Fqdn(strings.ToLower(domain)),
		ip:     listenIP,
	}, nil
}

// Start binds udp and tcp on every address, and blocks only long enough to
// confirm each listener actually came up.
//
// Both 127.0.0.1 (where /etc/resolver points the host) and the loopback alias
// (where sandbox microVMs reach us) have to be bound. The alias does not
// survive a reboot and is created by the daemon just before this runs.
func (s *Server) Start(port int, addrs ...net.IP) error {
	for _, addr := range addrs {
		for _, netw := range []string{"udp", "tcp"} {
			if err := s.listen(netw, net.JoinHostPort(addr.String(), fmt.Sprint(port))); err != nil {
				s.Stop()
				return err
			}
		}
	}
	return nil
}

func (s *Server) listen(netw, addr string) error {
	mux := dns.NewServeMux()
	mux.HandleFunc(".", s.handle)

	srv := &dns.Server{Addr: addr, Net: netw, Handler: mux}

	// started fires once the socket is bound, which lets us surface a bind
	// failure as a startup error instead of losing it to a background
	// goroutine. The old dnsmasq setup raced the loopback alias here and the
	// failure was invisible unless you went and read dnsmasq.err.log.
	started := make(chan struct{})
	errc := make(chan error, 1)
	srv.NotifyStartedFunc = func() { close(started) }

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			errc <- err
		}
	}()

	select {
	case <-started:
		s.mu.Lock()
		s.servers = append(s.servers, srv)
		s.mu.Unlock()
		return nil
	case err := <-errc:
		return fmt.Errorf("dns listen on %s/%s: %w", addr, netw, err)
	}
}

func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, srv := range s.servers {
		_ = srv.Shutdown()
	}
	s.servers = nil
}

func (s *Server) handle(w dns.ResponseWriter, req *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(req)
	m.Authoritative = true
	m.RecursionAvailable = false

	if len(req.Question) != 1 || req.Opcode != dns.OpcodeQuery {
		m.Rcode = dns.RcodeRefused
		_ = w.WriteMsg(m)
		return
	}

	q := req.Question[0]
	if !s.owns(q.Name) {
		m.Rcode = dns.RcodeRefused
		_ = w.WriteMsg(m)
		return
	}

	// A gets the alias. Everything else in the zone gets an empty NOERROR
	// rather than NXDOMAIN, so a client asking for AAAA falls straight through
	// to the A record instead of concluding the name doesn't exist.
	if q.Qtype == dns.TypeA || q.Qtype == dns.TypeANY {
		m.Answer = append(m.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 30},
			A:   s.ip,
		})
	}

	_ = w.WriteMsg(m)
}

func (s *Server) owns(name string) bool {
	name = strings.ToLower(dns.Fqdn(name))
	return name == s.domain || strings.HasSuffix(name, "."+s.domain)
}
