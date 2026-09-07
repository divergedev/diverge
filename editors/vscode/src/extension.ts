import * as vscode from "vscode";
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

  // Commands
  context.subscriptions.push(
    vscode.commands.registerCommand("diverge.refresh", () => {
      envsProvider.refresh();
      sessionsProvider.refresh();
      vscode.window.showInformationMessage("Diverge state refreshed");
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("diverge.runDoctor", async (item?: EnvironmentTreeItem) => {
      const envName = item?.env.name || (await vscode.window.showInputBox({
        prompt: "Enter environment name to diagnose (leave empty for all)",
      }));
      const terminal = vscode.window.createTerminal("Diverge Doctor");
      terminal.show();
      const arg = envName ? ` ${envName}` : "";
      terminal.sendText(`diverge doctor${arg}`);
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

      const terminal = vscode.window.createTerminal("Diverge Load Test");
      terminal.show();
      const rkArg = routingKey ? ` --routing-key ${routingKey} --baseline` : "";
      terminal.sendText(`diverge loadtest ${url}${rkArg}`);
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

      const terminal = vscode.window.createTerminal("Diverge Visual Diff");
      terminal.show();
      terminal.sendText(`diverge test visual --baseline ${baseline} --candidate ${candidate}`);
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
      const terminal = vscode.window.createTerminal("Diverge Dev");
      terminal.show();
      const sArg = service ? ` --service ${service}` : "";
      terminal.sendText(`diverge dev${sArg}`);
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("diverge.stopDev", async () => {
      const service = await vscode.window.showInputBox({
        prompt: "Enter service name to release lock for",
      });
      if (!service) return;
      const terminal = vscode.window.createTerminal("Diverge Dev Release");
      terminal.show();
      terminal.sendText(`diverge dev release ${service}`);
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("diverge.status", () => {
      const terminal = vscode.window.createTerminal("Diverge Status");
      terminal.show();
      terminal.sendText("diverge status");
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
    envsProvider.refresh();
    sessionsProvider.refresh();
  }, intervalSeconds * 1000);

  context.subscriptions.push({
    dispose: () => clearInterval(interval),
  });
}

export function deactivate() {}
