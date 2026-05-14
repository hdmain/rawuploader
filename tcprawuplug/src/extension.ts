import * as vscode from "vscode";
import { spawn } from "node:child_process";

const CODE_REGEX = /Your code:\s*(\d+)/i;

let statusBarItem: vscode.StatusBarItem | undefined;

function getStatusBarItem(): vscode.StatusBarItem {
  if (!statusBarItem) {
    statusBarItem = vscode.window.createStatusBarItem(
      vscode.StatusBarAlignment.Right,
      100
    );
    statusBarItem.command = "tcpraw.copyCode";
    statusBarItem.tooltip = "Click to copy tcpraw download code";
  }
  return statusBarItem;
}

function parseDownloadCode(output: string): string | undefined {
  const m = output.match(CODE_REGEX);
  return m?.[1];
}

async function runTcprawSend(filePath: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const child = spawn("tcpraw", ["send", filePath], {
      windowsHide: true,
    });
    let stdout = "";
    let stderr = "";
    child.stdout?.on("data", (d: Buffer) => {
      stdout += d.toString();
    });
    child.stderr?.on("data", (d: Buffer) => {
      stderr += d.toString();
    });
    child.on("error", (err) => {
      reject(err);
    });
    child.on("close", (code) => {
      const combined = stdout + stderr;
      if (code !== 0) {
        reject(
          new Error(
            combined.trim() ||
              `tcpraw exited with code ${code ?? "unknown"}. Is tcpraw on your PATH?`
          )
        );
      } else {
        resolve(combined);
      }
    });
  });
}

export function activate(context: vscode.ExtensionContext): void {
  let lastCode: string | undefined;

  context.subscriptions.push(
    vscode.commands.registerCommand("tcpraw.copyCode", async () => {
      if (!lastCode) {
        vscode.window.showInformationMessage("No tcpraw code to copy yet.");
        return;
      }
      await vscode.env.clipboard.writeText(lastCode);
      vscode.window.showInformationMessage(`Copied code: ${lastCode}`);
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand(
      "tcpraw.upload",
      async (uri: vscode.Uri | undefined) => {
        const target =
          uri ??
          vscode.window.activeTextEditor?.document.uri ??
          (await pickFileUri());
        if (!target || target.scheme !== "file") {
          vscode.window.showErrorMessage(
            "Select a file in Explorer or open a file, then try again."
          );
          return;
        }

        const fsPath = target.fsPath;
        try {
          const stat = await vscode.workspace.fs.stat(target);
          if (stat.type !== vscode.FileType.File) {
            vscode.window.showErrorMessage("tcpraw upload works on files only.");
            return;
          }
        } catch {
          vscode.window.showErrorMessage("Could not read that path.");
          return;
        }

        await vscode.window.withProgress(
          {
            location: vscode.ProgressLocation.Notification,
            title: "tcpraw",
            cancellable: false,
          },
          async (progress) => {
            progress.report({ message: "Sending…" });
            let output: string;
            try {
              output = await runTcprawSend(fsPath);
            } catch (e) {
              const msg = e instanceof Error ? e.message : String(e);
              vscode.window.showErrorMessage(`tcpraw failed: ${msg}`);
              return;
            }

            const code = parseDownloadCode(output);
            if (!code) {
              vscode.window.showWarningMessage(
                "tcpraw finished but no download code was found in the output."
              );
              return;
            }

            lastCode = code;
            const sb = getStatusBarItem();
            sb.text = `$(cloud-upload) tcpraw: ${code}`;
            sb.show();

            vscode.window.showInformationMessage(
              `File sent (encrypted). Code ${code} — click status bar to copy.`
            );
          }
        );
      }
    )
  );
}

async function pickFileUri(): Promise<vscode.Uri | undefined> {
  const picked = await vscode.window.showOpenDialog({
    canSelectFiles: true,
    canSelectFolders: false,
    canSelectMany: false,
    openLabel: "Upload with tcpraw",
  });
  return picked?.[0];
}

export function deactivate(): void {
  statusBarItem?.dispose();
  statusBarItem = undefined;
}
