# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What esb is

esb gives every Docker Sandbox microVM a real hostname behind a publicly trusted HTTPS certificate (`https://<label>.<domain>`). Sandboxes are microVMs, not containers, so they can't share a Docker network — each one only publishes to a host port. esb runs a DNS server and a Caddy proxy that make sandboxes reachable by name, with a wildcard cert issued via Let's Encrypt DNS-01 against deSEC.

## Commands

```
nix develop      # go, gopls, staticcheck, nixfmt
go build ./...
go test ./...
go test ./internal/proxy/...   # single package, e.g. the Caddyfile-render test
go vet ./...
nix build .#esb
go generate ./...   # regenerates internal/api/esbv1 stubs from proto/esb/v1/esb.proto (only needed if the proto changes; generated .pb.go files are checked in)
```

There's a Makefile that contains some common commands (build, install, lint, clean).

To have access to go and all other necessary tools, you *MUST* use nix.

## Architecture

esb is split into two halves that run as separate processes and communicate over a Unix socket:

- **The CLI** (`cmd/`, unprivileged, run by the user) — parses commands, sanitizes labels, shells out to the `sbx` binary for sandbox lifecycle, and calls the daemon over gRPC to reserve/release routes.
- **The daemon** (`internal/daemon`, root, launchd job) — owns the route table and the Caddy config. It runs the DNS server, an embedded Caddy instance, and the gRPC API. The CLI never touches shared config files; every change to routing goes through the daemon, which is why `esb up` needs no `sudo`.

Key packages:

- `internal/config` — loads `/etc/esb/config.json` (written by the nix-darwin module) or `$ESB_CONFIG`. Both CLI and daemon read the same file so they agree on domain, listen address, state dir, and port range. Nothing here is meant to be user-tunable at runtime.
- `internal/route` — the route table (`label` -> `hostPort`/`sandboxPort`). `Sanitize` collapses arbitrary input (e.g. `feature/lti-fix`) into a single DNS label, since the wildcard cert only covers `*.<domain>`, not deeper. `Store` persists to `routes.json` with write-then-rename, and deterministically derives a host port from the label (via FNV hash + probing) so a route's port survives sandbox restarts.
- `internal/proxy` — renders a Caddyfile from the route table and applies it to the embedded Caddy instance. Caddy is compiled in (imports `caddy/v2` + `github.com/caddy-dns/desec`), so this binary *is* the custom xcaddy build — there's no separate Caddy install to keep in sync.
- `internal/dnsd` — a minimal (~80 line) DNS server built on `github.com/miekg/dns`. Answers A records for `*.<domain>` with the loopback alias and REFUSEs everything else; never reads `/etc/resolv.conf` or `/etc/hosts`.
- `internal/netalias` — manages the loopback alias (`192.168.255.253` by default) that both the DNS server and Caddy bind to. It doesn't survive reboot, so the daemon re-adds it on every start.
- `internal/api` + `internal/api/esbv1` — the gRPC client/server wiring between CLI and daemon. `esbv1` holds generated protobuf/gRPC stubs from `proto/esb/v1/esb.proto`; `api` wraps them so callers work in `route.Route` rather than the wire type, keeping the on-disk JSON format independent of the wire format.
- `internal/sbx` — a thin wrapper that shells out to the real `sbx` (Docker Sandbox) CLI for create/ports/rm/template operations. esb never reimplements sandbox management.
- `cmd/` — the Cobra command tree (`up`, `route`, `down`, `urls`, `from-template`, `daemon`). `cmd/root.go`'s `load()` is the shared helper that resolves config and dials the daemon.

### Request flow example (`esb up`)

1. CLI sanitizes the label, checks the sandbox doesn't already exist via `sbx ls`.
2. CLI calls the daemon's `UpsertRoute` RPC to reserve a host port *before* creating the sandbox, because the port has to go in `sbx create --publish`.
3. CLI runs `sbx create --clone ...` with that port. On failure, it calls `Remove` on the route it just reserved so a route never points at a sandbox that doesn't exist.
4. The daemon, on every route mutation, re-renders the Caddyfile from the full route table and reloads Caddy (serialized via `Daemon.applyMu` so concurrent CLI calls can't race in a stale config).

### macOS specifics

esb binds Caddy to a specific loopback alias (`192.168.255.253`) rather than the wildcard to avoid conflicting with other processes that might be listening on `:443` — macOS allows both to coexist. Similarly the DNS server uses port `19353`, avoiding `53`.

### Testing notes

The test in `internal/proxy` renders a real Caddyfile and runs it through Caddy's config adapter, so a dropped plugin import (e.g. the deSEC DNS provider) fails at build/test time rather than at the next cert renewal. The nix package has `doCheck` on for the same reason.
