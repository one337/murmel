{
  description = "Murmel, a Mumble voice chat client for GNOME";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      base = nixpkgs.lib.removeSuffix "\n" (builtins.readFile ./VERSION);
      # Like tools/version.sh: the commit after the version, as the build
      # cannot tell whether it is the release tag.
      version = if self ? shortRev then "${base}+g${self.shortRev}" else "${base}-dev";
    in
    {
      packages = forAllSystems (pkgs: rec {
        murmel = pkgs.callPackage ./packaging/nix/package.nix {
          inherit version;
          src = self;
        };
        default = murmel;
      });
    };
}
