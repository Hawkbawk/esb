package adapter

import "testing"

// ParseStatic decides which of the two `usher up` forms the user meant, so it
// has to reject anything ambiguous with a machine name.
func TestParseStatic(t *testing.T) {
	good := map[string]struct {
		ip   string
		port int
	}{
		"10.0.0.5:8080":  {"10.0.0.5", 8080},
		"127.0.0.1:3000": {"127.0.0.1", 3000},
		"[::1]:3000":     {"::1", 3000},
	}
	for in, want := range good {
		ip, port, ok := ParseStatic(in)
		if !ok {
			t.Errorf("ParseStatic(%q) rejected a valid address", in)
			continue
		}
		if ip != want.ip || port != want.port {
			t.Errorf("ParseStatic(%q) = %q, %d; want %q, %d", in, ip, port, want.ip, want.port)
		}
	}

	bad := []string{
		"canvas-lms",      // a machine name
		"canvas-lms:3000", // a hostname, not an IP: too easy to confuse with a machine
		"10.0.0.5",        // no port
		"10.0.0.5:0",      // port out of range
		"10.0.0.5:70000",  // port out of range
		"10.0.0.5:http",   // named port
		"999.0.0.5:8080",  // not an IP
		"",
	}
	for _, in := range bad {
		if _, _, ok := ParseStatic(in); ok {
			t.Errorf("ParseStatic(%q) should have been rejected", in)
		}
	}
}
