import * as vscode from "vscode";
import { spawn } from "child_process";
import { DivergeCLI } from "./cli";
import { EnvironmentsProvider, EnvironmentTreeItem } from "./views/environments";
import { SessionsProvider } from "./views/sessions";
import { VerificationProvider } from "./views/verification";
import { DivergeStatusBar } from "./statusbar";
import { DivergeCodeLensProvider } from "./codelens";

export function activate(context: vscode.ExtensionContext) {
  const cli = new DivergeCLI();
  const statusBar = new DivergeStatusBar();
  context.subscriptions.push(statusBar);

  // Dedicated OutputChannel for secure non-shell command execution
  const outputChannel = vscode.window.createOutputChannel("Diverge");
  context.subscriptions.push(outputChannel);

  // Tree views
  const envsProvider = new EnvironmentsProvider(cli);
  const sessionsProvider = new SessionsProvider(cli);
  const verificationProvider = new VerificationProvider();

  vscode.window.registerTreeDataProvider("diverge-environments", envsProvider);
  vscode.window.registerTreeDataProvider("diverge-sessions", sessionsProvider);
  vscode.window.registerTreeDataProvider("diverge-verification", verificationProvider);

  // CodeLens
  context.subscriptions.push(
    vscode.languages.registerCodeLensProvider(
      [{ pattern: "**/diverge.yaml" }, { pattern: "**/.diverge.yaml" }],
      new DivergeCodeLensProvider()
    )
  );

  async function refreshAll() {
    envsProvider.refresh();
    sessionsProvider.refresh();
    try {
      const raw = await cli.run(["list", "--output", "json"]);
      const parsed = JSON.parse(raw);
      const items = Array.isArray(parsed) ? parsed : (parsed.items || []);
      if (items.length > 0) {
        statusBar.updateStatus(`$(rocket) Diverge (${items.length})`, `Diverge: ${items.length} active preview environment(s)`);
      } else {
        statusBar.updateStatus(`$(rocket) Diverge`, "Click to open Diverge Preview Actions");
      }
    } catch {
      statusBar.updateStatus(`$(rocket) Diverge`, "Click to open Diverge Preview Actions");
    }
  }

  function runCLI(args: string[], title: string) {
    outputChannel.show(true);
    outputChannel.appendLine(`\n=== Running: ${title} ===`);
    const cliPath = cli.getCliPath();
    const workspaceRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;

    const child = spawn(cliPath, args, { cwd: workspaceRoot });
    child.stdout.on("data", (data) => outputChannel.append(data.toString()));
    child.stderr.on("data", (data) => outputChannel.append(data.toString()));
    child.on("close", (code) => {
      outputChannel.appendLine(`=== ${title} finished with exit code ${code} ===\n`);
      if (code === 0) {
        refreshAll();
      }
    });
    child.on("error", (err) => {
      outputChannel.appendLine(`Error executing ${cliPath}: ${err.message}\n`);
    });
  }

  // Initial load
  refreshAll();

  // Commands
  context.subscriptions.push(
    vscode.commands.registerCommand("diverge.refresh", async () => {
      await refreshAll();
      vscode.window.showInformationMessage("Diverge state refreshed");
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("diverge.runDoctor", async (item?: EnvironmentTreeItem) => {
      const envName = item?.env.name || (await vscode.window.showInputBox({
        prompt: "Enter environment name to diagnose (leave empty for all)",
      }));
      const args = ["doctor"];
      if (envName) {
        args.push(envName);
      }
      runCLI(args, "Diverge Doctor");
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("diverge.runLoadtest", async () => {
      const url = await vscode.window.showInputBox({
        prompt: "Enter target URL for ephemeral load test",
        placeHolder: "https://api.preview.example.com",
      });
      if (!url) return;

      const routingKey = await vscode.window.showInputBox({
        prompt: "Enter preview routing key (optional)",
      });

      const args = ["loadtest", url];
      if (routingKey) {
        args.push("--routing-key", routingKey, "--baseline");
      }
      runCLI(args, "Diverge Load Test");
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("diverge.runVisualDiff", async () => {
      const baseline = await vscode.window.showInputBox({
        prompt: "Path to baseline screenshot",
        placeHolder: "./baseline.png",
      });
      if (!baseline) return;

      const candidate = await vscode.window.showInputBox({
        prompt: "Path to candidate screenshot",
        placeHolder: "./preview.png",
      });
      if (!candidate) return;

      runCLI(["test", "visual", "--baseline", baseline, "--candidate", candidate], "Diverge Visual Diff");
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("diverge.copyRoutingKey", (item?: EnvironmentTreeItem) => {
      if (item?.env.routingKey || item?.env.name) {
        const key = item.env.routingKey || item.env.name;
        vscode.env.clipboard.writeText(key);
        vscode.window.showInformationMessage(`Copied routing key "${key}" to clipboard`);
      }
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("diverge.openPreviewUrl", (item?: EnvironmentTreeItem) => {
      if (item?.env.url) {
        vscode.env.openExternal(vscode.Uri.parse(item.env.url));
      } else {
        vscode.window.showWarningMessage("No external URL found for this environment");
      }
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("diverge.startDev", async () => {
      const service = await vscode.window.showInputBox({
        prompt: "Enter service name to develop locally (leave empty for default)",
      });
      const args = ["dev"];
      if (service) {
        args.push("--service", service);
      }
      runCLI(args, "Diverge Dev");
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("diverge.stopDev", async () => {
      const service = await vscode.window.showInputBox({
        prompt: "Enter service name to release lock for",
      });
      if (!service) return;
      runCLI(["dev", "release", service], "Diverge Dev Release");
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("diverge.status", () => {
      runCLI(["status"], "Diverge Status");
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("diverge.showQuickMenu", async () => {
      const choice = await vscode.window.showQuickPick([
        { label: "$(refresh) Refresh Diverge State", action: "diverge.refresh" },
        { label: "$(play) Start Dev Session (diverge dev)", action: "diverge.startDev" },
        { label: "$(stop) Stop Dev Session (release)", action: "diverge.stopDev" },
        { label: "$(info) Show Environment Status", action: "diverge.status" },
        { label: "$(pulse) Run Doctor (Diagnostics)", action: "diverge.runDoctor" },
        { label: "$(dashboard) Run Load Benchmark", action: "diverge.runLoadtest" },
        { label: "$(diff) Run Visual Diff", action: "diverge.runVisualDiff" },
      ]);
      if (choice?.action) {
        vscode.commands.executeCommand(choice.action);
      }
    })
  );

  // Auto refresh interval
  const config = vscode.workspace.getConfiguration("diverge");
  const intervalSeconds = config.get<number>("autoRefreshInterval") || 10;
  const interval = setInterval(() => {
    refreshAll();
  }, intervalSeconds * 1000);

  context.subscriptions.push({
    dispose: () => clearInterval(interval),
  });
}

export function deactivate() {}
