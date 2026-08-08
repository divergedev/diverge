{
  description = "Nix dev shell for Diverge operator";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }: let
    supportedSystems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
    forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
  in {
    devShells = forAllSystems (system: let
      pkgs = import nixpkgs { inherit system; };
    in {
      default = pkgs.mkShell {
        buildInputs = with pkgs; [
          go_1_22 # Go 1.22 is available, falling back as 1.26 might not be available yet in nixpkgs, though project uses 1.26
          kubectl
          kubernetes-helm
          kustomize
          golangci-lint
        ];

        shellHook = ''
          echo "Welcome to the Diverge dev shell!"
        '';
      };
    });
  };
}
