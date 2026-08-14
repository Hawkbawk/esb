package adapter

import (
	"context"
	"fmt"
	"strconv"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"

	"github.com/hawkbawk/usher/internal/route"
)

// Docker routes to a port a running container already publishes.
//
// Docker has no way to add a port binding to an existing container, so this
// adapter is reuse-only: it finds a binding the user already asked for and
// points at it. Recreating the container to add one is possible but destroys
// anonymous volumes and loses anything not captured by inspect, so it isn't
// done silently.
type Docker struct{}

func (Docker) Kind() route.Adapter { return route.Docker }

func (Docker) Attach(ctx context.Context, machine string, port int) (route.Route, error) {
	cli, err := dockerClient(ctx)
	if err != nil {
		return route.Route{}, err
	}
	defer cli.Close()

	info, err := cli.ContainerInspect(ctx, machine)
	if err != nil {
		return route.Route{}, fmt.Errorf("inspecting container %q: %w", machine, err)
	}
	if info.State == nil || !info.State.Running {
		return route.Route{}, fmt.Errorf("container %q is not running", machine)
	}

	hostPort, err := publishedPort(info, port)
	if err != nil {
		return route.Route{}, fmt.Errorf("container %q: %w", machine, err)
	}

	// The host port is known, so the daemon skips allocation and forms the
	// upstream from it.
	return route.Route{
		Adapter:     route.Docker,
		Machine:     machine,
		MachinePort: port,
		HostPort:    hostPort,
	}, nil
}

func (Docker) Publish(context.Context, route.Route) error { return nil }

func (Docker) Detach(context.Context, route.Route, bool) error { return nil }

func (Docker) Destroy(ctx context.Context, machine string) error {
	cli, err := dockerClient(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()
	return cli.ContainerRemove(ctx, machine, container.RemoveOptions{Force: true})
}

// publishedPort finds the host port a container already publishes for
// containerPort/tcp, preferring a loopback binding since that's what the proxy
// dials.
func publishedPort(info container.InspectResponse, containerPort int) (int, error) {
	if info.NetworkSettings == nil {
		return 0, fmt.Errorf("no network settings")
	}

	key, err := nat.NewPort("tcp", strconv.Itoa(containerPort))
	if err != nil {
		return 0, err
	}
	bindings := info.NetworkSettings.Ports[key]
	if len(bindings) == 0 {
		return 0, fmt.Errorf("port %d/tcp is not published to the host.\n"+
			"Docker can't add a binding to a running container, so re-run it with "+
			"`-p %d:%d` and try again", containerPort, containerPort, containerPort)
	}

	// A wildcard binding works too, but 127.0.0.1 is what the proxy dials, so
	// prefer an explicit loopback one when the user set up both.
	best := bindings[0]
	for _, b := range bindings {
		if b.HostIP == "127.0.0.1" {
			best = b
			break
		}
	}

	hostPort, err := strconv.Atoi(best.HostPort)
	if err != nil {
		return 0, fmt.Errorf("unparseable host port %q for %s: %w", best.HostPort, key, err)
	}
	return hostPort, nil
}
