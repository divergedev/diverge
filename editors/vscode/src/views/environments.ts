import * as vscode from "vscode";
import { DivergeCLI } from "../cli";
import { EnvironmentItem } from "../types";

export class EnvironmentTreeItem extends vscode.TreeItem {
  constructor(
    public readonly env: EnvironmentItem,
    public readonly collapsibleState: vscode.TreeItemCollapsibleState
  ) {
    super(env.name, collapsibleState);
    this.tooltip = `${env.name} (${env.phase})`;
    this.description = env.url || env.phase;
    this.contextValue = "environment";

    if (env.phase === "Ready" || env.phase === "Running") {
      this.iconPath = new vscode.ThemeIcon("pass", new vscode.ThemeColor("testing.iconPassed"));
    } else if (env.phase === "Pending" || env.phase === "Deploying") {
      this.iconPath = new vscode.ThemeIcon("sync~spin", new vscode.ThemeColor("editorWarning.foreground"));
    } else {
      this.iconPath = new vscode.ThemeIcon("error", new vscode.ThemeColor("testing.iconFailed"));
    }
  }
}

export class EnvironmentDetailItem extends vscode.TreeItem {
  constructor(label: string, value: string, icon?: string) {
    super(`${label}: ${value}`, vscode.TreeItemCollapsibleState.None);
    if (icon) {
      this.iconPath = new vscode.ThemeIcon(icon);
    }
  }
}

export class EnvironmentsProvider implements vscode.TreeDataProvider<vscode.TreeItem> {
  private _onDidChangeTreeData: vscode.EventEmitter<vscode.TreeItem | undefined | void> = new vscode.EventEmitter<vscode.TreeItem | undefined | void>();
  readonly onDidChangeTreeData: vscode.Event<vscode.TreeItem | undefined | void> = this._onDidChangeTreeData.event;

  constructor(private cli: DivergeCLI) {}

  refresh(): void {
    this._onDidChangeTreeData.fire();
  }

  getTreeItem(element: vscode.TreeItem): vscode.TreeItem {
    return element;
  }

  async getChildren(element?: vscode.TreeItem): Promise<vscode.TreeItem[]> {
    if (!element) {
      try {
        const raw = await this.cli.run(["list", "-o", "json"]);
        const list = JSON.parse(raw);
        const items = list.items || [];
        const envs: EnvironmentItem[] = items.map((item: any) => ({
          name: item.metadata?.name || "unknown",
          namespace: item.metadata?.namespace || "default",
          phase: item.status?.phase || "Unknown",
          url: item.status?.url,
          routingKey: item.spec?.routing?.headerValue || item.metadata?.name,
          branch: item.spec?.source?.branch,
        }));
        if (!envs || envs.length === 0) {
          return [new vscode.TreeItem("No preview environments found")];
        }
        return envs.map((e) => new EnvironmentTreeItem(e, vscode.TreeItemCollapsibleState.Collapsed));
      } catch (err: any) {
        return [new vscode.TreeItem(`Error loading environments: ${err.message}`)];
      }
    }

    if (element instanceof EnvironmentTreeItem) {
      const env = element.env;
      const details: vscode.TreeItem[] = [
        new EnvironmentDetailItem("Phase", env.phase, "info"),
        new EnvironmentDetailItem("Routing Key", env.routingKey || env.name, "key"),
      ];
      if (env.url) {
        details.push(new EnvironmentDetailItem("Endpoint", env.url, "link"));
      }
      if (env.branch) {
        details.push(new EnvironmentDetailItem("Branch", env.branch, "git-branch"));
      }
      return details;
    }

    return [];
  }
}
