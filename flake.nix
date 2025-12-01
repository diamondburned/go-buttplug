{
  description = "A very basic flake";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
    flake-parts.url = "github:hercules-ci/flake-parts";

    intiface-engine = {
      url = "github:intiface/intiface-engine";
      flake = false;
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-parts,
      ...
    }@inputs:

    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = [
        "x86_64-linux"
        "x86_64-darwin"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      perSystem =
        {
          self',
          pkgs,
          lib,
          ...
        }:
        {
          devShells.default = pkgs.mkShell {
            packages =
              with pkgs;
              with self'.packages;
              [
                intiface-central
                intiface-engine

                # development tools
                go_latest
                gopls
                just
                jq
                moreutils # for sponge
                python3.pkgs.exrex # for schema generation
              ];

            GOEXPERIMENT = lib.concatStringsSep "," [
              # go-buttplug makes extensive use of encoding/json/v2 for
              # efficiency. As of Go 1.25, this is feature-gated.
              # See https://go.dev/doc/go1.25#json_v2.
              "jsonv2"
            ];
          };

          packages = {
            intiface-central = pkgs.writeShellScriptBin "intiface-central" ''
              exec ${lib.getExe pkgs.intiface-central} "$@"
            '';

            intiface-engine = pkgs.rustPlatform.buildRustPackage rec {
              pname = "intiface-engine";
              version = src.rev or "unknown";
              src = inputs.intiface-engine;

              cargoHash = "sha256-MJARQnGbRsjntiBW+3Mhh5TQVAs3tADlUBUb1/UTtec=";

              nativeBuildInputs = with pkgs; [
                # cmake
                pkg-config
              ];

              buildInputs = with pkgs; [
                udev
                dbus
                libusb1
              ];

              meta = {
                description = "Intiface CLI, except now also a library";
                homepage = "https://github.com/intiface/intiface-engine";
                mainProgram = "intiface-engine";
              };
            };
          };
        };
      flake = {
      };
    };
}
