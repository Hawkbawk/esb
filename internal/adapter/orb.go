package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/hawkbawk/usher/internal/route"
)

// orbDomain is the suffix OrbStack gives every VM. It answers over mDNS, so
// the name resolves from the host with no /etc/resolver entry involved.
const orbDomain = ".orb.local"

// resolveTimeout bounds the mDNS probe. Multicast DNS has no NXDOMAIN, so a
// lookup for a name nothing answers to hangs until it times out rather than
// failing fast.
const resolveTimeout = 2 * time.Second

// Orb routes to an OrbStack VM.
//
// The upstream is the VM's <name>.orb.local name rather than an address, so
// Caddy re-resolves on every dial and the route survives the VM restarting on
// a different IP. Note that the ip4 field of `orbctl info` is deliberately
// unused: that's the VM's address on OrbStack's internal bridge, and it is not
// reachable from the host.
type Orb struct{}

func (Orb) Kind() route.Adapter { return route.Orb }

func (Orb) Attach(ctx context.Context, machine string, port int) (route.Route, error) {
	if err := orbRunning(machine); err != nil {
		return route.Route{}, err
	}

	// Resolve once, purely as a check that the VM is actually answering. The
	// address is thrown away; Caddy does its own lookup at dial time.
	host := machine + orbDomain
	lookupCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	if _, err := net.DefaultResolver.LookupHost(lookupCtx, host); err != nil {
		return route.Route{}, fmt.Errorf("%s did not resolve: %w\n"+
			"The VM is running but isn't answering mDNS yet; try again in a moment", host, err)
	}

	return route.Route{
		Adapter:     route.Orb,
		Machine:     machine,
		MachinePort: port,
		Upstream:    net.JoinHostPort(host, strconv.Itoa(port)),
	}, nil
}

func (Orb) Publish(context.Context, route.Route) error { return nil }

func (Orb) Detach(context.Context, route.Route, bool) error { return nil }

func (Orb) Destroy(_ context.Context, machine string) error {
	return fmt.Errorf("usher will not delete an OrbStack VM; run `orbctl delete %s` if you mean it", machine)
}

// orbMachine is the subset of `orbctl list -f json` we care about.
type orbMachine struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// orbRunning checks the machine exists and is running. It uses orbctl rather
// than the mDNS probe because a lookup that times out can't distinguish "no
// such VM" from "VM is slow", but orbctl says which.
func orbRunning(machine string) error {
	cmd := exec.Command("orbctl", "list", "-f", "json")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("orbctl list: %w: %s", err, strings.TrimSpace(errb.String()))
	}

	var machines []orbMachine
	if err := json.Unmarshal(out.Bytes(), &machines); err != nil {
		return fmt.Errorf("parsing orbctl list output: %w", err)
	}

	for _, m := range machines {
		if m.Name != machine {
			continue
		}
		if m.State != "running" {
			return fmt.Errorf("OrbStack VM %q is %s, not running", machine, m.State)
		}
		return nil
	}

	names := make([]string, 0, len(machines))
	for _, m := range machines {
		names = append(names, m.Name)
	}
	if len(names) == 0 {
		return fmt.Errorf("no OrbStack VM named %q, and there are no VMs at all", machine)
	}
	return fmt.Errorf("no OrbStack VM named %q; have: %s", machine, strings.Join(names, ", "))
}
