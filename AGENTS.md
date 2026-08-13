# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What usher is

usher gives anything running locally a real hostname behind a publicly trusted HTTPS certificate (`https://<hostname>.<domain>`). It runs a DNS server and a Caddy proxy, with a wildcard cert issued via Let's Encrypt DNS-01 against deSEC. An *adapter* decides how to reach a given target: a Docker Sandbox microVM, an OrbStack VM, a running container, or a bare `ip:port`.

usher never creates or manages the machines themselves — it only adds names on top of ones that are already running.

## Commands

```
nix develop      # go, gopls, staticcheck, nixfmt
go build ./...
go test ./...
go test ./internal/proxy/...   # single package, e.g. the Caddyfile-render test
go vet ./...
nix build .#usher
go generate ./...   # regenerates internal/api/usherv1 stubs from proto/usher/v1/usher.proto (only needed if the proto changes; generated .pb.go files are checked in)
```

There's a Makefile that contains some common commands (build, install, lint, clean).

To have access to go and all other necessary tools, you *MUST* use nix.

## Architecture

usher is split into two halves that run as separate processes and communicate over a Unix socket:

- **The CLI** (`cmd/`, unprivileged, run by the user) — parses commands, sanitizes hostnames, runs the adapters, and calls the daemon over gRPC to reserve/release routes.
- **The daemon** (`internal/daemon`, root, launchd job) — owns the route table and the Caddy config. It runs the DNS server, an embedded Caddy instance, and the gRPC API. The CLI never touches shared config files; every change to routing goes through the daemon, which is why `usher up` needs no `sudo`.

The daemon is deliberately generic: it stores a hostname, an upstream, and which adapter put it there, and never shells out to `sbx`, `orbctl`, or Docker. All adapter-specific work happens in the CLI before the RPC. Adding an adapter should mean one new file in `internal/adapter` and one new enum value, nothing in the daemon.

Key packages:

- `internal/config` — loads `/etc/usher/config.json` (written by the nix-darwin module) or `$USHER_CONFIG`. Both CLI and daemon read the same file so they agree on domain, listen address, state dir, and port range. Nothing here is meant to be user-tunable at runtime.
- `internal/route` — the route table. A `Route` is `Host` -> `Upstream`, plus the `Adapter`/`Machine`/`MachinePort` that produced it and an optional `HostPort`. `Sanitize` collapses arbitrary input into a single DNS label, since the wildcard cert only covers `*.<domain>`, not deeper — it applies to hostnames only, never to machine names. `Store` persists to `routes.json` with write-then-rename. `Upsert` has three branches: caller-supplied upstream (orb, static), caller-supplied host port (docker), or allocate one (sbx). Allocation derives a port from the hostname via FNV hash + probing, so a route's port survives a machine restart; routes sharing an `(adapter, machine, machinePort)` triple share one host port.
- `internal/adapter` — one file per adapter behind a four-verb interface (`Attach`, `Publish`, `Detach`, `Destroy`). `Attach` validates the machine and describes the route; `Publish` runs afterward, and only `sbx` needs it, because only the daemon can decide which host port to publish on.
- `internal/proxy` — renders a Caddyfile from the route table and applies it to the embedded Caddy instance. Caddy is compiled in (imports `caddy/v2` + `github.com/caddy-dns/desec`), so this binary *is* the custom xcaddy build — there's no separate Caddy install to keep in sync.
- `internal/dnsd` — a minimal (~80 line) DNS server built on `github.com/miekg/dns`. Answers A records for `*.<domain>` with the loopback alias and REFUSEs everything else; never reads `/etc/resolv.conf` or `/etc/hosts`.
- `internal/netalias` — manages the loopback alias (`192.168.255.253` by default) that both the DNS server and Caddy bind to. It doesn't survive reboot, so the daemon re-adds it on every start.
- `internal/api` + `internal/api/usherv1` — the gRPC client/server wiring between CLI and daemon. `usherv1` holds generated protobuf/gRPC stubs from `proto/usher/v1/usher.proto`; `api` wraps them so callers work in `route.Route` rather than the wire type, keeping the on-disk JSON format independent of the wire format. `adapterMap` there is the single table mapping the wire enum to the stored string.
- `internal/sbx` — a thin wrapper that shells out to the real `sbx` (Docker Sandbox) CLI. usher never reimplements sandbox management.
- `cmd/` — the Cobra command tree (`up`, `down`, `urls`, `daemon`). `cmd/root.go`'s `load()` is the shared helper that resolves config and dials the daemon.

### Adapter notes

- **orb** — the upstream is the `<machine>.orb.local` *name*, not an address, so Caddy re-resolves on every dial and the route survives the VM restarting on a new IP. Do not use `orbctl info`'s `ip4` field: that's the VM's address on OrbStack's internal bridge and it is not reachable from the host. `.orb.local` resolves over mDNS; Go on darwin always sends `.local` lookups through the system resolver, so `net.DefaultResolver` works with no `/etc/resolver` entry. mDNS has no NXDOMAIN, so always bound those lookups with a timeout.
- **docker** — reuse-only. Docker can't add a port binding to a running container, and recreating one loses anonymous volumes, so if the port isn't published usher errors out and tells the user to re-run with `-p`. Uses the moby SDK (`github.com/docker/docker@v28.5.2+incompatible`) via `client.FromEnv` + `WithAPIVersionNegotiation`, which reaches OrbStack's daemon fine. Note that `github.com/docker/docker/api` and `.../client` now resolve to `moby/moby` submodules — pin the monolithic `+incompatible` version, don't let `go mod tidy` pick those up.

### Request flow example (`usher up`)

1. CLI parses the args into an adapter, a machine, a port, and a sanitized hostname (or recognizes the `ip:port` static form).
2. `Attach` validates the machine exists and is running, and returns a partially filled `route.Route`.
3. CLI calls the daemon's `UpsertRoute` RPC. The daemon allocates a host port if the route arrived without an upstream — that has to happen before the sbx publish, since the port goes in `sbx ports --publish`.
4. CLI calls `Publish` with the stored route. On failure it calls `Remove` so a route never points at something unreachable.
5. The daemon, on every route mutation, re-renders the Caddyfile from the full route table and reloads Caddy (serialized via `Daemon.applyMu` so concurrent CLI calls can't race in a stale config).

### macOS specifics

usher binds Caddy to a specific loopback alias (`192.168.255.253`) rather than the wildcard to avoid conflicting with other processes that might be listening on `:443` — macOS allows both to coexist. Similarly the DNS server uses port `19353`, avoiding `53`.

### Testing notes

The test in `internal/proxy` renders a real Caddyfile with one route per adapter kind and runs it through Caddy's config adapter, so a dropped plugin import (e.g. the deSEC DNS provider) fails at build/test time rather than at the next cert renewal. The nix package has `doCheck` on for the same reason.
