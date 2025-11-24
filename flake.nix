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
                go
                gopls
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
                # openssl
                udev
                # libusb1
                dbus
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
