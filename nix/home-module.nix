self:
{
  config,
  lib,
  pkgs,
  ...
}:
# The user-facing half of usher: just the CLI.
#
# The daemon it talks to is a root launchd job configured by
# nix/darwin-module.nix. They share nothing but /etc/usher/config.json and the
# unix socket, so this module needs no options beyond which build to install.
let
  cfg = config.programs.usher;
in
{
  options.programs.usher = {
    enable = lib.mkEnableOption "the usher routing CLI";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.usher;
      defaultText = lib.literalExpression "self.packages.\${pkgs.stdenv.hostPlatform.system}.usher";
      description = "The usher package to install.";
    };

    enableFishCompletion = lib.mkOption {
      type = lib.types.bool;
      default = config.programs.fish.enable or false;
      defaultText = lib.literalExpression "config.programs.fish.enable";
      description = "Install cobra-generated fish completions for usher.";
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [ cfg.package ];

    # Generated at build time rather than shipped, so the completions can never
    # drift from the command tree.
    xdg.configFile."fish/completions/usher.fish" = lib.mkIf cfg.enableFishCompletion {
      source =
        pkgs.runCommand "usher.fish"
          {
            nativeBuildInputs = [ cfg.package ];
          }
          ''
            usher completion fish > "$out"
          '';
    };
  };
}
