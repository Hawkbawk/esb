{
  description = "esb - extended sandbox: name-based HTTPS routing for Docker Sandbox microVMs";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs =
    { self, nixpkgs, ... }:
    let
      systems = [
        "aarch64-darwin"
        "x86_64-darwin"
        "aarch64-linux"
        "x86_64-linux"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});

      mkEsb =
        { lib, buildGoModule }:
        buildGoModule (finalAttrs: {
          pname = "esb";
          version = "0.1.0";

          src = lib.cleanSource ./.;

          vendorHash = "sha256-LrUnpzGu8oPoq1SxHgWVylEryRmcMPCJvAnGGir33fk=";

          ldflags = [
            "-s"
            "-w"
            "-X=github.com/hawkbawk/esb/cmd.version=${finalAttrs.version}"
          ];

          # The Caddyfile adapter test is what proves the deSEC plugin is still wired
          # up, so it is worth paying for on every build.
          doCheck = true;

          # Deliberately not wrapped with a PATH: esb shells out to `sbx`, `docker`,
          # and `git`, all of which come from Homebrew or Docker Desktop on this
          # machine. Pinning them to nixpkgs copies would find the wrong binaries.

          meta = {
            description = "Name-based HTTPS routing for Docker Sandbox microVMs";
            longDescription = ''
              esb (extended sandbox) gives every Docker Sandbox a real hostname behind
              a publicly trusted wildcard certificate. It embeds Caddy with the deSEC
              DNS-01 provider and a minimal authoritative DNS server, so there is no
              separate Caddy build and no dnsmasq to install.
            '';
            homepage = "https://github.com/hawkbawk/esb";
            license = lib.licenses.mit;
            mainProgram = "esb";
            platforms = lib.platforms.unix;
          };
        });
    in
    {
      packages = forAllSystems (pkgs: rec {
        esb = pkgs.callPackage mkEsb { };
        default = esb;
      });

      overlays.default = final: _prev: {
        esb = final.callPackage mkEsb { };
      };

      # The CLI half. Installs the binary for a user.
      homeModules.esb = import ./nix/home-module.nix self;

      # The daemon half. Needs root to bind 443, add the loopback alias, and
      # write /etc/resolver, so it cannot live in home-manager.
      darwinModules.esb = import ./nix/darwin-module.nix self;

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.gopls
            pkgs.go-tools
            pkgs.nixfmt
            pkgs.nix-update

            # For `go generate ./...`, which regenerates the daemon API stubs
            # from proto/esb/v1/esb.proto. The generated .pb.go files are
            # checked in, so a plain `nix build` never needs these.
            pkgs.protobuf
            pkgs.protoc-gen-go
            pkgs.protoc-gen-go-grpc
          ];
        };
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt);
    };
}
