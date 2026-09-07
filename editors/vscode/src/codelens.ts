import * as vscode from "vscode";

export class DivergeCodeLensProvider implements vscode.CodeLensProvider {
  provideCodeLenses(
    document: vscode.TextDocument,
    _token: vscode.CancellationToken
  ): vscode.CodeLens[] | Thenable<vscode.CodeLens[]> {
    const codeLenses: vscode.CodeLens[] = [];
    const text = document.getText();
    const lines = text.split("\n");

    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];

      // Match top-level services: or service name entries
      if (line.match(/^\s*-\s*name:\s*([a-zA-Z0-9_-]+)/)) {
        const range = new vscode.Range(i, 0, i, line.length);

        codeLenses.push(
          new vscode.CodeLens(range, {
            title: "▶ Run Load Test",
            command: "diverge.runLoadtest",
          })
        );

        codeLenses.push(
          new vscode.CodeLens(range, {
            title: "🩺 Diagnose with Doctor",
            command: "diverge.runDoctor",
          })
        );
      }
    }

    return codeLenses;
  }
}
