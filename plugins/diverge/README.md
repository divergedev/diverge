# Diverge Antigravity Plugin (`plugins/diverge`)

Official **Google Antigravity** plugin bundle for [Diverge](https://divergedev.com) — packaging the Diverge MCP server (`diverge mcp`), workflow skill (`diverge`), specialist operator subagent (`diverge-operator`), and workspace validation rules (`rules/AGENTS.md`) into a single toggleable unit.

## What's Included

| Component | Path | Description |
| :--- | :--- | :--- |
| **Plugin Manifest** | `plugin.json` | Declares the `diverge` plugin, icon, and starter prompts. |
| **MCP Server** | `mcp_config.json` | Registers `diverge-mcp` (`diverge mcp`) providing 21 tools for `Environment`, `PreviewGroup`, `AgentTask`, `diverge_doctor`, `diverge_loadtest`, and `diverge_visual_test`. |
| **Skill** | `skills/diverge/SKILL.md` | Progressive-disclosure runbook for `.diverge.yaml` validation, delta deploys, `diverge curl --compare-baseline`, Magic Links (`?_diverge_env=`), and `AgentTask` steering. |
| **Subagent** | `agents/diverge-operator.md` | Specialist `diverge-operator` agent for `DIV-101`–`DIV-110` troubleshooting and baseline-vs-preview verification. |
| **Rules** | `rules/AGENTS.md` | Contextual rules enforcing `.diverge.yaml` schema validation and `divergedev.com/v1alpha1` conventions. |

## Installation

### 1. Automatic Workspace Discovery (Zero-Install)
When you open a repository containing `.agents/plugins.json` pointing to `plugins`, Antigravity automatically discovers and activates the `diverge` plugin.

### 2. Global Installation (All Workspaces)
Copy or symlink this directory into your global Antigravity plugins folder and restart Antigravity:

```bash
mkdir -p ~/.gemini/config/plugins
cp -R plugins/diverge ~/.gemini/config/plugins/diverge
```

### 3. Antigravity IDE UI Extension (`.vsix` / Open VSX)
Because Antigravity IDE uses the VS Code / Open VSX extension host, the Diverge sidebar extension in [`editors/vscode/`](../../editors/vscode/package.json) (`divergedev.diverge-vscode`) installs directly into Antigravity IDE:

```bash
cd editors/vscode
npm run package:vsix
```
