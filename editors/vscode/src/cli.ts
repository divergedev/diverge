import * as vscode from "vscode";
import { execFile } from "child_process";
import { promisify } from "util";

const execFileAsync = promisify(execFile);

export class DivergeCLI {
  private getCliPath(): string {
    const config = vscode.workspace.getConfiguration("diverge");
    return config.get<string>("cliPath") || "diverge";
  }

  public async run(args: string[]): Promise<string> {
    const cli = this.getCliPath();
    const workspaceRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;

    try {
      const { stdout } = await execFileAsync(cli, args, { cwd: workspaceRoot });
      return stdout.trim();
    } catch (err: any) {
      const stderr = err.stderr ? err.stderr.toString() : err.message;
      throw new Error(`Diverge CLI command failed (${cli} ${args.join(" ")}): ${stderr}`);
    }
  }

  public async runJSON<T>(args: string[]): Promise<T> {
    const output = await this.run([...args, "--json"]);
    try {
      return JSON.parse(output) as T;
    } catch (e) {
      throw new Error(`Failed to parse JSON output: ${output}`);
    }
  }
}
