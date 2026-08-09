self:
{
  config,
  lib,
  pkgs,
  ...
}:
# The user-facing half of esb: just the CLI.
#
# The daemon it talks to is a root launchd job configured by
# nix/darwin-module.nix. They share nothing but /etc/esb/config.json and the
# unix socket, so this module needs no options beyond which build to install.
let
  cfg = config.programs.esb;
in
{
  options.programs.esb = {
    enable = lib.mkEnableOption "the esb sandbox routing CLI";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.esb;
      defaultText = lib.literalExpression "self.packages.\${pkgs.stdenv.hostPlatform.system}.esb";
      description = "The esb package to install.";
    };

    enableFishCompletion = lib.mkOption {
      type = lib.types.bool;
      default = config.programs.fish.enable or false;
      defaultText = lib.literalExpression "config.programs.fish.enable";
      description = "Install cobra-generated fish completions for esb.";
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [ cfg.package ];

    # Generated at build time rather than shipped, so the completions can never
    # drift from the command tree.
    xdg.configFile."fish/completions/esb.fish" = lib.mkIf cfg.enableFishCompletion {
      source =
        pkgs.runCommand "esb.fish"
          {
            nativeBuildInputs = [ cfg.package ];
          }
          ''
            esb completion fish > "$out"
          '';
    };
  };
}
