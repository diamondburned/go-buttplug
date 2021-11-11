{ pkgs ? import <nixpkgs> {} }:

let lib = pkgs.lib;
	stdenv = pkgs.stdenv;
	
	intiface-cli = stdenv.mkDerivation rec {
		name = "intiface-cli";
		version = "v40";
	
		src = pkgs.fetchzip {
			url = "https://github.com/intiface/intiface-cli-rs/releases/download/${version}/intiface-cli-rs-linux-x64-Release.zip";
			sha256 = "sha256:01c9vd2qi9d57fx3py0802fyzra1rsgf3nxp9gnxflqvy3vnskkw";
			stripRoot = false;
		};
	
		nativeBuildInputs = with pkgs; [
			autoPatchelfHook
		];
	
		buildInputs = with pkgs; [
			libudev
			libusb1
			dbus
			openssl
		];
		
		sourceRoot = ".";
		
		installPhase = ''
			install -m755 -D $src/IntifaceCLI $out/bin/intiface-cli
		'';
	};

in pkgs.mkShell {
	name = "go-buttplug";

	buildInputs = with pkgs; [
		intiface-cli
		go
	];
}
