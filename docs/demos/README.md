# Diverge Terminal Demos & Visual Assets

This directory contains reproducible [Charmbracelet VHS](https://github.com/charmbracelet/vhs) scripts used to record terminal workflows and keep Diverge's documentation visuals up to date.

## Tape Files

| Tape Script | Recorded Workflow | Output Asset |
| :--- | :--- | :--- |
| [`quickstart.tape`](./quickstart.tape) | `diverge init --config`, `diverge validate`, and `diverge quickstart --local --dry-run` | `docs/assets/terminal-quickstart-demo.gif` |
| [`trace-and-diff.tape`](./trace-and-diff.tape) | `diverge diff --base origin/main`, `diverge preview create --dry-run`, and `diverge trace pr-42` | `docs/assets/terminal-trace-and-diff.gif` |
| [`agent-sandbox.tape`](./agent-sandbox.tape) | `kubectl apply -f config/samples/agenttask_autonomous.yaml`, `AgentTask` status inspection, and `POST /api/v1/agenttask/meter` | `docs/assets/terminal-agent-sandbox.gif` |

## Vector SVG Diagrams (`docs/assets/`)

In addition to VHS GIF outputs, high-DPI vector SVG diagrams live in [`docs/assets/`](../assets/) so they render crisply at any resolution on GitHub and the docs site (`divergedev.github.io/public/images/`):

- [`docs/assets/terminal-quickstart-demo.svg`](../assets/terminal-quickstart-demo.svg) — Animated CSS `@keyframes` macOS terminal session (`diverge init`, `diverge diff`, `diverge preview create`, `diverge trace pr-42`) with full static fallback.
- [`docs/assets/delta-vs-duplicated.svg`](../assets/delta-vs-duplicated.svg) — Side-by-side comparison of **Traditional Full-Stack Duplication** (60 pods, 5 DB instances, ~$2,400/mo) vs. **Diverge Delta Preview (`PreviewGroup`)** (17 pods, 1 shared Postgres with 5 instant schemas, ~$340/mo).
- [`docs/assets/architecture-overview.svg`](../assets/architecture-overview.svg) — End-to-end request and control-plane architecture covering L7 header/baggage routing, `activator-proxy` scale-to-zero splash, delta microservice topology, PostgreSQL/Atlas + OpenFeature/Flipt + Kafka/Temporal isolation, and autonomous `AgentTask` sandboxes.

## Regenerating Terminal Demos

### Prerequisites

Install `vhs`, `ttyd`, and `ffmpeg` (or enter the repository's Nix development shell):

```bash
# macOS (Homebrew)
brew install vhs

# Or build the diverge CLI inside the Nix shell
nix develop -c go build -o bin/diverge ./cmd/diverge
export PATH="$PWD/bin:$PATH"
```

### Recording a Tape

Run `vhs` from the repository root:

```bash
vhs docs/demos/quickstart.tape
vhs docs/demos/trace-and-diff.tape
vhs docs/demos/agent-sandbox.tape
```

## Where Assets Are Embedded

- **Main Repository (`divergedev/diverge`)**:
  - [`README.md`](../../README.md) — Embeds `docs/assets/terminal-quickstart-demo.svg`, `docs/assets/delta-vs-duplicated.svg`, and `docs/assets/architecture-overview.svg`.
  - [`docs/architecture.md`](../architecture.md) — Embeds `./assets/architecture-overview.svg` and `./assets/delta-vs-duplicated.svg`.
- **Documentation Website (`divergedev/divergedev.github.io`)**:
  - `public/images/terminal-quickstart-demo.svg` (embedded in `src/content/docs/index.mdx` and `src/content/docs/overview.md`)
  - `public/images/delta-vs-duplicated.svg` (embedded in `src/content/docs/index.mdx` and `src/content/docs/overview.md`)
  - `public/images/architecture-overview.svg` (embedded in `src/content/docs/architecture/overview.md`)
