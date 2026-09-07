import * as vscode from "vscode";

export class ActionTreeItem extends vscode.TreeItem {
  constructor(label: string, commandId: string, icon: string, tooltip: string) {
    super(label, vscode.TreeItemCollapsibleState.None);
    this.iconPath = new vscode.ThemeIcon(icon);
    this.tooltip = tooltip;
    this.command = {
      command: commandId,
      title: label,
    };
  }
}

export class VerificationProvider implements vscode.TreeDataProvider<vscode.TreeItem> {
  getTreeItem(element: vscode.TreeItem): vscode.TreeItem {
    return element;
  }

  async getChildren(element?: vscode.TreeItem): Promise<vscode.TreeItem[]> {
    if (element) {
      return [];
    }

    return [
      new ActionTreeItem(
        "Run Ephemeral Load Test",
        "diverge.runLoadtest",
        "dashboard",
        "Benchmark latency (p50/p95/p99) and error rates with optional baseline comparison"
      ),
      new ActionTreeItem(
        "Run Visual Regression Diff",
        "diverge.runVisualDiff",
        "diff",
        "Detect pixel-level UI drift and generate HTML visual reports"
      ),
      new ActionTreeItem(
        "Run Diverge Doctor",
        "diverge.runDoctor",
        "pulse",
        "Diagnose pod crash loops, unprogrammed HTTPRoutes, and workload issues"
      ),
    ];
  }
}
