{
  description = "A cascading stream forwarding unit for scalable, distributed voice and video conferencing over Matrix";

  inputs = {
    flake-utils.url = github:numtide/flake-utils;
    gomod2nix.url = github:nix-community/gomod2nix;
  };

  outputs = { self, nixpkgs, flake-utils, gomod2nix }:
    (flake-utils.lib.eachDefaultSystem
      (system:
        let
          pkgs = import nixpkgs {
            inherit system;
            overlays = [ gomod2nix.overlays.default ];
          };
          waterfall = pkgs.buildGoApplication {
            pname = "waterfall";
            version = "0.1";
            pwd = ./.;
            src = ./.;
            modules = ./gomod2nix.toml;
            CGO_ENABLED = 0;
            ldflags = [ "-s" "-w" ];
          };

        in
        {
          packages = {
            default = waterfall;
            docker = pkgs.dockerTools.buildLayeredImage {
              name = "waterfall";
              config.Cmd = [ "${waterfall}/bin/cmd/sfu" ];
            };
          };
          devShells.default = pkgs.mkShell {
            packages = [
              (pkgs.mkGoEnv { pwd = ./.; })
              pkgs.gomod2nix
            ];
          };
        })
    );
}
