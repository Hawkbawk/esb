// Package netalias manages the loopback alias the proxy and DNS server bind.
package netalias

import (
	"fmt"
	"net"
	"os/exec"
	"time"
)

// Ensure adds the alias to lo0 if it isn't there and waits for the kernel to
// finish attaching it.
//
// The alias does not survive a reboot and nothing else on the system re-adds
// it, so the daemon creates it rather than trusting whatever ran last.
// ifconfig can return before the address is actually usable, and a bind a
// fraction of a second too early fails with "can't assign requested address",
// so poll until it really shows up.
func Ensure(addr net.IP) error {
	if present(addr) {
		return nil
	}

	// Already-exists is not an error worth reporting; the poll below is the
	// real check either way.
	cmd := exec.Command("/sbin/ifconfig", "lo0", "alias", addr.String(), "255.255.255.255")
	out, err := cmd.CombinedOutput()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if present(addr) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	if err != nil {
		return fmt.Errorf("adding lo0 alias %s: %w: %s", addr, err, out)
	}
	return fmt.Errorf("lo0 alias %s did not come up within 5s", addr)
}

func present(addr net.IP) bool {
	iface, err := net.InterfaceByName("lo0")
	if err != nil {
		return false
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.Equal(addr) {
			return true
		}
	}
	return false
}
