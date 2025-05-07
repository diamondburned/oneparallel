{
  description = "A very basic flake";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        devShells.default = pkgs.mkShell {
          inputsFrom = [
            self.packages.${system}.default
          ];

          packages = with pkgs; [
            gopls
            gotools
            nixfmt-rfc-style
          ];

          GOEXPERIMENT = "synctest"; # only for running tests
        };

        packages = rec {
          default = oneparallel;

          oneparallel = pkgs.buildGoModule {
            pname = "oneparallel";
            src = self;
            version = self.rev or "unknown";

            vendorHash = "sha256-hsp4m5dGBpKl6axs05KK0JSJWG2Ncwu4kisgH2R7E+U=";

            meta = with pkgs.lib; {
              description = "Human-friendly parallelization tool similar to GNU/Moreutils Parallel.";
              homepage = "https://libdb.so/oneparallel";
              license = licenses.mit;
              mainProgram = "oneparallel";
            };
          };
        };
      }
    );
}
