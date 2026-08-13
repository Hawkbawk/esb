# usher.

It shows every request to the right door. usher gives anything running locally a real hostname behind a publicly trusted HTTPS certificate.

```
usher up canvas-lms 3000 canvas --adapter orb
# -> https://canvas.sbx.example.dedyn.io
```

## Why.

Everything you run locally ends up behind an ugly `localhost:31847` URL, with no way for two of those things to reach each other over verifiable HTTPS. Canvas cares a lot about the hostname it's reached at, and LTI wants real TLS, so ports aren't good enough.

usher runs a DNS server and a Caddy proxy that together make anything reachable at `https://<hostname>.<domain>`. The wildcard cert comes from Let's Encrypt through a DNS-01 challenge against deSEC, so it's publicly trusted. That means there's no CA to install anywhere, and calls between your machines verify without disabling TLS checks.

usher never creates or manages the machines themselves. It only adds names on top of ones that are already running.

## How it fits together.

```
browser / another machine
        |
        v  DNS: *.<domain> -> 192.168.255.253
   usher daemon (root launchd job)
     |-- DNS server on 127.0.0.1:19353 and the loopback alias
     |-- Caddy on 192.168.255.253:443, wildcard cert via deSEC DNS-01
     `-- unix socket API at <stateDir>/usher.sock
              ^
              |  route add / remove
        usher CLI (no sudo)
              |
              `-- adapters: sbx, orbctl, the Docker API
```

Four pieces are worth calling out.

**Caddy is compiled in.** `internal/proxy` imports `caddy/v2` plus `github.com/caddy-dns/desec`, which is exactly what an [xcaddy](https://github.com/caddyserver/xcaddy) build generates. So this binary *is* the custom Caddy build. There's no second Caddy to install, and no risk of the CLI and the daemon disagreeing about which plugins are available.

**There's no dnsmasq.** `internal/dnsd` is about 80 lines of `github.com/miekg/dns`. It answers A records for `*.<domain>` with the loopback alias and REFUSEs everything else. It never reads `/etc/resolv.conf` or `/etc/hosts`, so it can't accidentally turn into a general-purpose resolver.

**The daemon owns the route table.** The CLI asks for changes over a unix socket instead of writing config fragments to a shared directory. That's why `usher up` needs no sudo, and why config generation and reloading happen in one place.

**Adapters live entirely in the CLI.** The daemon stores a hostname, an upstream, and which adapter put it there — it never shells out to `sbx`, `orbctl`, or Docker itself. That keeps the privileged half small, and makes a new adapter one new file in `internal/adapter`.

## Adapters.

| Adapter | A machine name means | How usher reaches it |
| --- | --- | --- |
| `sbx` | a Docker Sandbox microVM | publishes a host port with `sbx ports`, proxies to `127.0.0.1:<hostPort>` |
| `orb` | an OrbStack VM | proxies to `<machine>.orb.local:<port>` |
| `docker` | a running container | reuses a port the container already publishes |
| — | an `<ip>:<port>` you gave directly | proxies straight there |

The orb adapter deliberately proxies to the `.orb.local` *name* rather than an address, so Caddy re-resolves on every dial and a route survives the VM restarting on a different IP. (`orbctl info` reports an `ip4` on OrbStack's internal bridge, which isn't reachable from the host at all — don't use it.)

The docker adapter is reuse-only. Docker can't add a port binding to a running container, so if the port isn't published yet, usher tells you to re-run the container with `-p` rather than silently recreating it and losing its anonymous volumes.

## Commands.

| Command | What it does |
| --- | --- |
| `usher up <machine> <port> <hostname> [-a sbx\|orb\|docker]` | Routes a hostname to a port on a running machine. |
| `usher up <ip>:<port> <hostname>` | Routes a hostname straight to an address. |
| `usher down <hostname> [--destroy]` | Removes a hostname's route. `--destroy` also tears down the machine. |
| `usher urls` | Lists every route and where it points. |
| `usher daemon` | Runs the DNS server and proxy in the foreground. This is the launchd job. |
| `usher daemon config` | Prints the Caddyfile the daemon would generate. Handy for debugging. |
| `usher daemon install` / `uninstall` | Sets up the config, state dirs, resolver, and launchd job without nix. |

A machine can have as many hostnames as you like, all at once — the proxy doesn't care. `usher down` only ever removes the one hostname you named; the machine and every other route pointing at it keep working.

Hostnames are sanitized into a single DNS label, because the wildcard cert covers `*.<domain>` and nothing deeper. So `feature/lti-fix` becomes `feature-lti-fix`, and `canvas.foo.<domain>` would not work. Machine names are left alone, since container and VM names legitimately contain characters a DNS label can't.

Host ports (for `sbx`) are derived from the hostname rather than left ephemeral, and a hostname keeps its port forever once assigned. That way a route stays correct across machine restarts. Routes pointing at the same port on the same machine share one published host port.

## Kits.

`kits/usher-networking` is a Docker Sandbox [mixin kit](https://docs.docker.com/ai/sandboxes/customize/kit-reference/) that tells the agent inside a sandbox how usher routing works: bind to `0.0.0.0` rather than loopback, since the host port forward reaches the sandbox's network interface, not its loopback. Reference it locally in your sandbox config:

```json
{"kits": ["https://raw.githubusercontent.com/hawkbawk/usher/main/kits/usher-networking", ...]}
```

or point `--kit` at it directly on `sbx create`.

## Installing it.

Add the flake as an input:

```nix
{
  inputs.usher.url = "github:hawkbawk/usher";
}
```

The daemon needs root — it binds 443, adds the loopback alias, and writes `/etc/resolver` — so it's a nix-darwin module, not a home-manager one. The CLI is the home-manager half.

```nix
# darwin-configuration.nix
{
  imports = [ inputs.usher.darwinModules.usher ];

  services.usher = {
    enable = true;
    domain = "sbx.example.dedyn.io";
    acmeEmail = "you@example.com";
    tokenFile = config.sops.secrets.desec_token.path;
  };
}
```

```nix
# home.nix
{
  imports = [ inputs.usher.homeModules.usher ];
  programs.usher.enable = true;
}
```

Not using nix? `sudo usher daemon install --domain <domain> --token-file <path>` does the same setup by hand, and `sudo usher daemon uninstall --purge` undoes it.

You need a deSEC zone. The free tier at [desec.io](https://desec.io) gives you `<name>.dedyn.io` with full API access, which is all this needs. Create an API token and put it wherever `tokenFile` points. deSEC is only the proof channel: Caddy writes a TXT record to show it controls the zone, then deletes it.

## Configuration.

The nix-darwin module writes `/etc/usher/config.json`, which both halves read. Set `$USHER_CONFIG` to point somewhere else if you're not using the module.

| Option | Default | Notes |
| --- | --- | --- |
| `domain` | — | Required. Must be a deSEC zone you control. |
| `listenAddress` | `192.168.255.253` | Not `.254`, which OrbStack uses. |
| `dnsPort` | `19353` | Not 53, and not 19321, which OrbStack's resolver owns. |
| `stateDir` | `/usr/local/var/usher` | Route table, certs, logs, and the socket. |
| `portRange.from` / `.to` | `30000` / `39999` | Host ports handed out to sandboxes. |
| `acmeEmail` | `""` | Optional, for expiry notices. |
| `tokenFile` | `/run/secrets/desec_token` | Where sops-nix puts it. |

## Notes on macOS.

OrbStack already holds `*:443`, which is why Caddy binds a specific alias instead of the wildcard. macOS lets a specific-address bind coexist with an existing wildcard bind, so OrbStack's own `.orb.local` and `inst.test` routing keeps working.

The loopback alias doesn't survive a reboot and nothing else re-adds it, so the daemon creates it on every start and waits for the kernel to finish attaching it before binding anything.

`.orb.local` names resolve over mDNS, not unicast DNS, so `dig` won't find them but `dscacheutil` and Go's default resolver will. Go on darwin always routes `.local` lookups through the system resolver, which is why the orb adapter works without any `/etc/resolver` entry.

## Developing.

```
nix develop      # go, gopls, staticcheck, nixfmt
go test ./...
nix build .#usher
```

The test in `internal/proxy` renders the Caddyfile and runs it through Caddy's adapter, with one route per adapter kind. If the deSEC plugin import ever gets dropped, that fails at build time rather than the next time a cert needs renewing. `doCheck` is on in the derivation for the same reason.
