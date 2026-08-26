{
  description = "Nix dev shell for Diverge — environment-as-a-service engine for Kubernetes";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            # Go
            go
            gopls
            delve

            # Kubernetes
            kubectl
            kubernetes-helm
            kustomize
            kind

            # Tools
            gh
            jq
            yq-go
            lefthook
            buf
            protoc-gen-go
            protoc-gen-connect-go
            air
            editorconfig-checker
            gitleaks
            yamllint

            # Container
            docker-client

            # Web dashboard
            nodejs_22
          ];

          shellHook = ''
            export GOPATH="$HOME/go"
            export PATH="$GOPATH/bin:$PATH"
            echo ""
            echo "🔀 Diverge dev shell loaded"
            echo "   Go:      $(go version | cut -d' ' -f3)"
            echo "   kubectl: $(kubectl version --client -o json 2>/dev/null | jq -r '.clientVersion.gitVersion' 2>/dev/null || echo 'n/a')"
            echo "   helm:    $(helm version --short 2>/dev/null || echo 'n/a')"
            echo ""
            lefthook install 2>/dev/null || true
          '';
        };
      }
    );
}
