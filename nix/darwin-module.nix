self:
{
  config,
  lib,
  pkgs,
  ...
}:
# The daemon half of esb: name-based HTTPS routing for Docker Sandbox microVMs.
#
# The problem this solves: sandboxes are microVMs, not containers, so they
# can't share a Docker network. Each one publishes its app to a host port,
# which gives you ugly localhost:31847 URLs and no way for two sandboxes to
# talk to each other over verifiable HTTPS. Canvas cares a lot about the
# hostname it's reached at (multi-tenancy) and LTI wants real TLS, so ports
# aren't good enough.
#
# The design:
#   * A dedicated loopback alias (listenAddress) reachable both from the host
#     and from inside every sandbox microVM. Sandboxes route to the host by
#     default, so a host-local IP resolves identically on both sides.
#   * esb's own DNS server answers *.<domain> with that alias. Resolution is
#     entirely local; no public A record ever points at a private address.
#   * Caddy, compiled into the esb binary, binds 443 on the alias and routes by
#     Host header to whichever host port a given sandbox published to.
#   * A single wildcard cert from Let's Encrypt via DNS-01 against deSEC. deSEC
#     is only the proof channel: Caddy writes a TXT record to prove it controls
#     the zone, then deletes it. Because the cert is publicly trusted, there is
#     no CA to install in the host or in any sandbox image, which is the whole
#     reason we aren't using a private CA or OrbStack's.
#
# Note on the wildcard cert: it covers exactly one label, so
# `canvas-foo.<domain>` works but `canvas.foo.<domain>` does not. esb sanitises
# worktree names into a single label.
#
# OrbStack already holds *:443, which is why Caddy binds a specific alias
# rather than the wildcard. macOS allows a specific-address bind alongside an
# existing wildcard bind, so the two coexist and OrbStack's own .orb.local and
# inst.test routing keeps working untouched.
let
  cfg = config.services.esb;

  esbConfig = pkgs.writeText "esb-config.json" (
    builtins.toJSON {
      inherit (cfg)
        domain
        listenAddress
        dnsPort
        stateDir
        tokenFile
        acmeEmail
        ;
      portMin = cfg.portRange.from;
      portMax = cfg.portRange.to;
    }
  );
in
{
  options.services.esb = {
    enable = lib.mkEnableOption "name-based HTTPS routing for Docker Sandboxes";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.esb;
      defaultText = lib.literalExpression "self.packages.\${pkgs.stdenv.hostPlatform.system}.esb";
      description = "The esb package providing the daemon.";
    };

    domain = lib.mkOption {
      type = lib.types.str;
      example = "sbx.hawkbawk.dedyn.io";
      description = ''
        Domain that sandbox hostnames live directly under, so a sandbox
        labelled `canvas-foo` is reachable at `canvas-foo.<domain>`. Must be a
        zone you control in deSEC, since Caddy needs to write TXT records
        under it to get a wildcard cert.
      '';
    };

    listenAddress = lib.mkOption {
      type = lib.types.str;
      default = "192.168.255.253";
      description = ''
        Loopback alias that `<label>.<domain>` resolves to and that Caddy binds
        443 on. Deliberately not 192.168.255.254, which OrbStack already uses
        for its own domain routing.
      '';
    };

    dnsPort = lib.mkOption {
      type = lib.types.port;
      default = 19353;
      description = ''
        Port the esb DNS server listens on. Not 53, because /etc/resolver files
        can name a port and binding 53 would fight with everything else on the
        machine. Not 19321 either, which OrbStack's resolver owns.
      '';
    };

    portRange = {
      from = lib.mkOption {
        type = lib.types.port;
        default = 30000;
        description = "Lowest host port esb will publish a sandbox on.";
      };
      to = lib.mkOption {
        type = lib.types.port;
        default = 39999;
        description = "Highest host port esb will publish a sandbox on.";
      };
    };

    stateDir = lib.mkOption {
      type = lib.types.str;
      default = "/usr/local/var/esb";
      description = ''
        Holds the route table, the ACME account key and wildcard cert, the
        daemon logs, and the unix socket the CLI connects to.
      '';
    };

    acmeEmail = lib.mkOption {
      type = lib.types.str;
      default = "";
      description = "Optional ACME account email for certificate expiry notices.";
    };

    tokenFile = lib.mkOption {
      type = lib.types.str;
      default = "/run/secrets/desec_token";
      description = ''
        File containing the deSEC API token, read by root at daemon start.
        Defaults to where sops-nix puts it.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = !(lib.hasInfix "CHANGEME" cfg.domain) && cfg.domain != "";
        message = ''
          services.esb.domain is unset or still the placeholder value.
          Register a free subdomain at https://desec.io (the free tier gives you
          <name>.dedyn.io with full API access), then set it.
        '';
      }
      {
        assertion = cfg.portRange.from < cfg.portRange.to;
        message = "services.esb.portRange.from must be below portRange.to.";
      }
    ];

    environment = {
      systemPackages = [ cfg.package ];

      etc."esb/config.json".source = esbConfig;

      # Consulted by the host resolver, and - because the sandbox runtime
      # proxies DNS out to the host - by every sandbox microVM too. That is why
      # sandboxes need no /etc/resolv.conf or /etc/hosts seeding at boot.
      etc."resolver/${cfg.domain}".text = ''
        nameserver 127.0.0.1
        port ${toString cfg.dnsPort}
      '';
    };

    system.activationScripts.postActivation.text = ''
      echo "setting up esb state under ${cfg.stateDir}"
      mkdir -p ${cfg.stateDir}/caddy ${cfg.stateDir}/log

      # The daemon re-adds this on start; do it here too so the alias exists
      # right after a rebuild without waiting for a service restart.
      /sbin/ifconfig lo0 alias ${cfg.listenAddress} 255.255.255.255 || true
    '';

    launchd.daemons.esb-daemon = {
      serviceConfig = {
        ProgramArguments = [
          "${lib.getExe cfg.package}"
          "daemon"
        ];
        RunAtLoad = true;
        KeepAlive = true;
        StandardOutPath = "${cfg.stateDir}/log/esb.log";
        StandardErrorPath = "${cfg.stateDir}/log/esb.err.log";
        EnvironmentVariables = {
          # Caddy's storage path is set explicitly in the generated config, but
          # certmagic still consults HOME in a few places.
          HOME = "/var/root";
        };
      };
    };
  };
}
