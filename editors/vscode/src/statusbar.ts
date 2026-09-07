import * as vscode from "vscode";

export class DivergeStatusBar implements vscode.Disposable {
  private statusBarItem: vscode.StatusBarItem;

  constructor() {
    this.statusBarItem = vscode.window.createStatusBarItem(
      vscode.StatusBarAlignment.Left,
      100
    );
    this.statusBarItem.command = "diverge.showQuickMenu";
    this.statusBarItem.text = "$(rocket) Diverge";
    this.statusBarItem.tooltip = "Click to open Diverge Preview Actions";
    this.statusBarItem.show();
  }

  public updateStatus(text: string, tooltip?: string): void {
    this.statusBarItem.text = text;
    if (tooltip) {
      this.statusBarItem.tooltip = tooltip;
    }
  }

  public dispose(): void {
    this.statusBarItem.dispose();
  }
}
