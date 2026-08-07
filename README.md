# esb.

Extended sandbox. It gives every Docker Sandbox microVM a real hostname behind a publicly trusted HTTPS certificate.

```
esb up canvas-lti-fix 3000 ~/dev/inst/canvas-lms
# -> https://canvas-lti-fix.sbx.example.dedyn.io
```

## Why.

Sandboxes are microVMs, not containers, so they can't share a Docker network. Each one publishes its app to a host port, which leaves you with ugly `localhost:31847` URLs and no way for two sandboxes to reach each other over verifiable HTTPS. Canvas cares a lot about the hostname it's reached at, and LTI wants real TLS, so ports aren't good enough.

esb runs a DNS server and a Caddy proxy that together make every sandbox reachable at `https://<label>.<domain>`. The wildcard cert comes from Let's Encrypt through a DNS-01 challenge against deSEC, so it's publicly trusted. That means there's no CA to install in the host or in any sandbox image, and cross-sandbox calls verify without disabling TLS checks.

## How it fits together.

```
browser / other sandbox
        |
        v  DNS: *.<domain> -> 192.168.255.253
   esb daemon (root launchd job)
     |-- DNS server on 127.0.0.1:19353 and the loopback alias
     |-- Caddy on 192.168.255.253:443, wildcard cert via deSEC DNS-01
     `-- unix socket API at <stateDir>/esb.sock
              ^
              |  route add / remove
        esb CLI (no sudo)
              |
              `-- shells out to sbx for create, ports, rm, and template load
```

Three pieces are worth calling out.

**Caddy is compiled in.** `internal/proxy` imports `caddy/v2` plus `github.com/caddy-dns/desec`, which is exactly what an [xcaddy](https://github.com/caddyserver/xcaddy) build generates. So this binary *is* the custom Caddy build. There's no second Caddy to install, and no risk of the CLI and the daemon disagreeing about which plugins are available.

**There's no dnsmasq.** `internal/dnsd` is about 80 lines of `github.com/miekg/dns`. It answers A records for `*.<domain>` with the loopback alias and REFUSEs everything else. It never reads `/etc/resolv.conf` or `/etc/hosts`, so it can't accidentally turn into a general-purpose resolver.

**The daemon owns the route table.** The CLI asks for changes over a unix socket instead of writing config fragments to a shared directory. That's why `esb up` needs no sudo, and why config generation and reloading happen in one place.

## Commands.

| Command | What it does |
| --- | --- |
| `esb up <label> <port> <workspace>` | Creates a sandbox in `--clone` mode and routes it. |
| `esb route <label> <port>` | Routes a port on a sandbox that already exists. |
| `esb down <label>` | Removes the sandbox and its route. |
| `esb urls` | Lists routed sandboxes and their URLs. |
| `esb from-template [dir]` | Builds a template image from a repo Dockerfile, loads it, and creates a sandbox. |
| `esb daemon` | Runs the DNS server and proxy in the foreground. This is the launchd job. |
| `esb daemon config` | Prints the Caddyfile the daemon would generate. Handy for debugging. |

`esb up` uses `--clone`, so the agent works on a private in-container clone and its commits come back out through the `sandbox-<label>` git remote. Nothing the agent does can touch your worktree.

Labels are sanitized into a single DNS label, because the wildcard cert covers `*.<domain>` and nothing deeper. So `feature/lti-fix` becomes `feature-lti-fix`, and `canvas.foo.<domain>` would not work.

Host ports are derived from the label rather than left ephemeral, and a label keeps its port forever once assigned. That way a route stays correct across sandbox restarts.

## Installing it.

Add the flake as an input:

```nix
{
  inputs.esb.url = "github:rhawk/esb";
}
```

The daemon needs root — it binds 443, adds the loopback alias, and writes `/etc/resolver` — so it's a nix-darwin module, not a home-manager one. The CLI is the home-manager half.

```nix
# darwin-configuration.nix
{
  imports = [ inputs.esb.darwinModules.esb ];

  services.esb = {
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
  imports = [ inputs.esb.homeModules.esb ];
  programs.esb.enable = true;
}
```

You need a deSEC zone. The free tier at [desec.io](https://desec.io) gives you `<name>.dedyn.io` with full API access, which is all this needs. Create an API token and put it wherever `tokenFile` points. deSEC is only the proof channel: Caddy writes a TXT record to show it controls the zone, then deletes it.

## Configuration.

The nix-darwin module writes `/etc/esb/config.json`, which both halves read. Set `$ESB_CONFIG` to point somewhere else if you're not using the module.

| Option | Default | Notes |
| --- | --- | --- |
| `domain` | — | Required. Must be a deSEC zone you control. |
| `listenAddress` | `192.168.255.253` | Not `.254`, which OrbStack uses. |
| `dnsPort` | `19353` | Not 53, and not 19321, which OrbStack's resolver owns. |
| `stateDir` | `/usr/local/var/esb` | Route table, certs, logs, and the socket. |
| `portRange.from` / `.to` | `30000` / `39999` | Host ports handed out to sandboxes. |
| `acmeEmail` | `""` | Optional, for expiry notices. |
| `tokenFile` | `/run/secrets/desec_token` | Where sops-nix puts it. |

## Notes on macOS.

OrbStack already holds `*:443`, which is why Caddy binds a specific alias instead of the wildcard. macOS lets a specific-address bind coexist with an existing wildcard bind, so OrbStack's own `.orb.local` and `inst.test` routing keeps working.

The loopback alias doesn't survive a reboot and nothing else re-adds it, so the daemon creates it on every start and waits for the kernel to finish attaching it before binding anything.

## Developing.

```
nix develop      # go, gopls, staticcheck, nixfmt
go test ./...
nix build .#esb
```

The test in `internal/proxy` renders the Caddyfile and runs it through Caddy's adapter. If the deSEC plugin import ever gets dropped, that fails at build time rather than the next time a cert needs renewing. `doCheck` is on in the derivation for the same reason.
