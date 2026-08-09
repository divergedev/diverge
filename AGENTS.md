# Diverge AI Agent Context (AGENTS.md)

This document provides context and guidelines for AI coding assistants and agents working on the Diverge codebase.

## Project Overview

Diverge is a Kubernetes operator and environment-as-a-service engine that creates ephemeral preview environments triggered by merge request events. It supports delta deployments, configurable database provisioning, and Istio-based header routing.

## Architecture

The project consists of three main binaries:
1. **Controller (`cmd/controller`)**: A Kubernetes operator built with `controller-runtime`. It watches for `Environment` Custom Resources and provisions necessary resources (like Argo CD `Application` CRs, databases).
2. **Proxy (`cmd/proxy`)**: A reverse proxy that helps route traffic to specific preview environments based on headers.
3. **CLI (`cmd/diverge`)**: A command-line interface built with `cobra` for managing environments (`create`, `list`, `delete`, `open`, `logs`, `validate`, `status`, `version`, `init`).

## Key Packages

- `cmd/`: Entrypoints for the three binaries.
- `internal/cli/`: Implementation of the Cobra CLI commands.
- `charts/diverge/`: The Helm chart for deploying Diverge.

## Build System

- **Nix**: We use Nix for managing development environments (`flake.nix`). **All terminal commands must be run inside the Nix dev shell by prefixing with `nix develop -c`.**
- **Make**: Standard targets include `make install` (install CRDs), `make run` (run operator locally), `make test`, and `make build`.
- **Go**: Version 1.26.

## Test Patterns

- We use `testify/assert` and `testify/require` for assertions.
- We favor **table-driven tests** for comprehensive coverage.
- We use `httptest` for testing HTTP servers/clients.
- We use Property-Based Testing (PBT) using `rapid` where specified or appropriate.
- Currently passing 117 tests.

## Conventions

- **Linting**: Code must pass `golangci-lint run` and `go vet`.
- **Commits**: Use conventional commit messages (`feat: ...`, `fix: ...`, `docs: ...`). Do **NOT** use `--no-verify` with git commit.
- **Hooks**: We use `lefthook` for pre-commit hooks.
- **Idiomatic Go**: Write standard, idiomatic Go code with proper error handling.

## CI/CD

- **GitHub Actions**: Workflows defined in `.github/workflows/` (e.g., `ci.yml`).
- **Goreleaser**: Used for building and releasing the project (`.goreleaser.yaml`). It builds the three binaries and packages them into a single consolidated Docker image (`ghcr.io/divergedev/diverge`).

## Open Issues / Coming Next

- **WebSocket Support** (#6) — Full WebSocket proxying for real-time preview environments
- **Controller EnvTest + E2E** (#9) — Comprehensive controller integration tests with envtest
- **Environment Proto + ConnectRPC API** (#12) — gRPC/ConnectRPC API server for environment management
- **CI Actions Node 22 Bump** (#25) — Migrate GitHub Actions to Node 22 runtime
- **Godoc Coverage** (#26) — Complete documentation for all exported functions
