import * as vscode from "vscode";
import { exec } from "child_process";
import { promisify } from "util";

const execAsync = promisify(exec);

export class DivergeCLI {
  private getCliPath(): string {
    const config = vscode.workspace.getConfiguration("diverge");
    return config.get<string>("cliPath") || "diverge";
  }

  public async run(args: string[]): Promise<string> {
    const cli = this.getCliPath();
    const command = `${cli} ${args.join(" ")}`;
    const workspaceRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;

    try {
      const { stdout } = await execAsync(command, { cwd: workspaceRoot });
      return stdout.trim();
    } catch (err: any) {
      const stderr = err.stderr ? err.stderr.toString() : err.message;
      throw new Error(`Diverge CLI command failed (${command}): ${stderr}`);
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
