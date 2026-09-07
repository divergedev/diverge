import * as vscode from "vscode";
import { DivergeCLI } from "../cli";
import { DevSessionItem } from "../types";

export class DevSessionTreeItem extends vscode.TreeItem {
  constructor(public readonly session: DevSessionItem) {
    super(`${session.service} (${session.developer})`, vscode.TreeItemCollapsibleState.None);
    this.tooltip = `Service: ${session.service}\nDeveloper: ${session.developer}\nBranch: ${session.branch}\nStatus: ${session.status}`;
    this.description = session.branch;
    this.contextValue = "devSession";

    if (session.status === "ACTIVE") {
      this.iconPath = new vscode.ThemeIcon("lock", new vscode.ThemeColor("charts.orange"));
    } else {
      this.iconPath = new vscode.ThemeIcon("history", new vscode.ThemeColor("disabledForeground"));
    }
  }
}

export class SessionsProvider implements vscode.TreeDataProvider<vscode.TreeItem> {
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
    if (element) {
      return [];
    }

    try {
      const sessions = await this.cli.runJSON<DevSessionItem[]>(["dev", "sessions"]);
      if (!sessions || sessions.length === 0) {
        return [new vscode.TreeItem("No active dev session locks")];
      }
      return sessions.map((s) => new DevSessionTreeItem(s));
    } catch (err: any) {
      return [new vscode.TreeItem(`Error loading sessions: ${err.message}`)];
    }
  }
}
