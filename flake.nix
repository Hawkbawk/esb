{
  description = "esb - extended sandbox: name-based HTTPS routing for Docker Sandbox microVMs";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs =
    { nixpkgs, ... }:
    let
      systems = [
        "aarch64-darwin"
        "x86_64-darwin"
        "aarch64-linux"
        "x86_64-linux"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAllSystems (pkgs: rec {
        esb = pkgs.callPackage ./nix/package.nix { };
        default = esb;
      });

      overlays.default = final: _prev: {
        esb = final.callPackage ./nix/package.nix { };
      };

      # The CLI half. Installs the binary for a user.
      homeModules.esb = import ./nix/home-module.nix;

      # The daemon half. Needs root to bind 443, add the loopback alias, and
      # write /etc/resolver, so it cannot live in home-manager.
      darwinModules.esb = import ./nix/darwin-module.nix;

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.gopls
            pkgs.go-tools
            pkgs.nixfmt

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
