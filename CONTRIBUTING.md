# Contributing to Diverge

Welcome! We are excited to have you contribute to Diverge, an environment-as-a-service engine for Kubernetes.

## Prerequisites
Before you begin, ensure you have the following installed:
- Go 1.22+
- Docker
- kubectl
- kind
- nix (optional but recommended)

## Development Setup
1. Clone the repository:
   ```bash
   git clone https://github.com/divergedev/diverge.git
   cd diverge
   ```
2. Set up your environment (if using Nix or Direnv):
   ```bash
   direnv allow
   # or
   nix develop
   ```
3. Build and test the project:
   ```bash
   make build
   make test
   ```

## Making Changes
1. Fork the repository.
2. Create a new feature branch for your changes.
3. Make your changes in the codebase.
4. Run formatting, tests, and linting before committing:
   ```bash
   make fmt
   make test
   make lint
   ```

## Pull Request Process
- Use descriptive titles for your Pull Requests that follow [Conventional Commits](https://www.conventionalcommits.org/).
- Provide a clear description of what the PR does and why the changes are needed.
- Link any relevant GitHub issues to your PR.

## Code Style
- Follow standard Go conventions.
- Run `golangci-lint` to ensure code quality.
- Write tests for any new features or bug fixes.

## License
Diverge is licensed under the Apache 2.0 License. By contributing to this project, you agree that your contributions will be licensed under the same license.

## Getting Help
Found a bug or want to request a feature? Please open an issue on our [GitHub Issues](https://github.com/divergedev/diverge/issues).
