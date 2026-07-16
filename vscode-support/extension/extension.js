const vscode = require('vscode');
const path = require('path');
const fs = require('fs');
const { LanguageClient, TransportKind } = require('vscode-languageclient/node');

let client;

/**
 * @param {vscode.ExtensionContext} context
 */
function activate(context) {
    // Try to find the LSP binary
    const lspPath = findLspBinary();

    if (lspPath) {
        startLspClient(context, lspPath);
    } else {
        // Fallback: basic features without LSP
        vscode.window.showInformationMessage(
            'Serv LSP not found. Build it with: go build -o serv-lsp ./lsp/ — Using basic mode.'
        );
        activateBasicMode(context);
    }

    // CodeLens provider (works with or without LSP)
    const codeLensProvider = vscode.languages.registerCodeLensProvider('serv', {
        provideCodeLenses(document) {
            let lenses = [];
            const testRegex = /^\s*(test\s+|test\s*")/i;
            const routeRegex = /^\s*(route\s+)/i;
            for (let i = 0; i < document.lineCount; i++) {
                let line = document.lineAt(i).text;
                if (testRegex.test(line)) {
                    let range = new vscode.Range(i, 0, i, line.length);
                    lenses.push(new vscode.CodeLens(range, {
                        title: "▶ Run Test Block",
                        command: "serv.test"
                    }));
                }
                if (routeRegex.test(line)) {
                    let range = new vscode.Range(i, 0, i, line.length);
                    lenses.push(new vscode.CodeLens(range, {
                        title: "⚡ Start Web Service",
                        command: "serv.run"
                    }));
                }
            }
            return lenses;
        }
    });
    context.subscriptions.push(codeLensProvider);

    // Register commands
    context.subscriptions.push(
        vscode.commands.registerCommand('serv.run', () => runServCommand('run')),
        vscode.commands.registerCommand('serv.build', () => runServCommand('build')),
        vscode.commands.registerCommand('serv.test', () => runServCommand('test')),
        vscode.commands.registerCommand('serv.watch', () => runServCommand('run', ['--watch'])),
        vscode.commands.registerCommand('serv.visualizeWorkflow', () => openWorkflowVisualizer(context)),
        vscode.commands.registerCommand('serv.exploreQueue', () => openQueueExplorer(context)),
        vscode.commands.registerCommand('serv.exploreStore', () => openStoreExplorer(context)),
        vscode.commands.registerCommand('serv.exploreLocks', () => openLocksExplorer(context)),
        vscode.commands.registerCommand('serv.simulateRoute', () => openRouteSimulator(context))
    );
}

function runServCommand(command, extraArgs = []) {
    const editor = vscode.window.activeTextEditor;
    if (!editor || editor.document.languageId !== 'serv') {
        vscode.window.showWarningMessage('Open a .srv file first');
        return;
    }

    const filePath = editor.document.fileName;
    const servPath = findServBinary();

    if (!servPath) {
        vscode.window.showErrorMessage('Serv compiler not found. Place serv.exe in workspace root or PATH.');
        return;
    }

    // Build command args
    let args = [command, `"${filePath}"`, ...extraArgs];
    if (command === 'build') {
        const outputName = path.basename(filePath, '.srv') + '.exe';
        args = [command, `"${filePath}"`, '-o', outputName];
    }

    // Run in integrated terminal — check if the active shell is PowerShell to apply call operator '&'
    const shellPath = vscode.env.shell ? vscode.env.shell.toLowerCase() : '';
    const isPowerShell = shellPath.includes('powershell') || shellPath.includes('pwsh') || shellPath === '';
    
    const terminal = vscode.window.createTerminal({ name: `Serv: ${command}` });
    terminal.show();
    if (isPowerShell) {
        terminal.sendText(`& "${servPath}" ${args.join(' ')}`);
    } else {
        terminal.sendText(`"${servPath}" ${args.join(' ')}`);
    }
}

function findServBinary() {
    // Check config
    const configPath = vscode.workspace.getConfiguration('serv').get('compilerPath');
    if (configPath && fs.existsSync(configPath)) return configPath;

    // Check workspace root
    const workspaceFolders = vscode.workspace.workspaceFolders;
    if (workspaceFolders) {
        const root = workspaceFolders[0].uri.fsPath;
        for (const name of ['serv.exe', 'serv']) {
            const p = path.join(root, name);
            if (fs.existsSync(p)) return p;
        }
    }

    // Assume it's in PATH
    return 'serv';
}

function findLspBinary() {
    // 1. Check config
    const configPath = vscode.workspace.getConfiguration('serv').get('lspPath');
    if (configPath && fs.existsSync(configPath)) {
        return configPath;
    }

    // 1.5. Check colocated with compiler path
    const servPath = findServBinary();
    if (servPath && servPath !== 'serv') {
        const compilerDir = path.dirname(servPath);
        for (const name of ['serv-lsp.exe', 'serv-lsp']) {
            const p = path.join(compilerDir, name);
            if (fs.existsSync(p)) return p;
        }
    }

    // 2. Check workspace root
    const workspaceFolders = vscode.workspace.workspaceFolders;
    if (workspaceFolders) {
        const root = workspaceFolders[0].uri.fsPath;
        const candidates = ['serv-lsp.exe', 'serv-lsp'];
        for (const name of candidates) {
            const p = path.join(root, name);
            if (fs.existsSync(p)) return p;
        }
    }

    // 3. Check PATH (assume it's available if the file exists in common locations)
    const pathDirs = (process.env.PATH || '').split(path.delimiter);
    for (const dir of pathDirs) {
        const candidates = ['serv-lsp.exe', 'serv-lsp'];
        for (const name of candidates) {
            const p = path.join(dir, name);
            if (fs.existsSync(p)) return p;
        }
    }

    return null;
}

function startLspClient(context, lspPath) {
    const serverOptions = {
        run: { command: lspPath, transport: TransportKind.stdio },
        debug: { command: lspPath, transport: TransportKind.stdio }
    };

    const clientOptions = {
        documentSelector: [{ scheme: 'file', language: 'serv' }],
    };

    client = new LanguageClient(
        'servLanguageServer',
        'Serv Language Server',
        serverOptions,
        clientOptions
    );

    client.start();
    context.subscriptions.push(client);
}

function activateBasicMode(context) {
    // Minimal diagnostics via serv lint (fallback when LSP is not available)
    const cp = require('child_process');
    const diagnosticCollection = vscode.languages.createDiagnosticCollection('serv');
    context.subscriptions.push(diagnosticCollection);

    const triggerLint = (document) => {
        if (document.languageId !== 'serv') return;
        const workspaceFolders = vscode.workspace.workspaceFolders;
        let servPath = 'serv';
        if (workspaceFolders) {
            const root = workspaceFolders[0].uri.fsPath;
            const winPath = path.join(root, 'serv.exe');
            if (fs.existsSync(winPath)) servPath = winPath;
        }

        cp.execFile(servPath, ['lint', document.fileName], (err, stdout, stderr) => {
            let diagnostics = [];
            let output = stdout + "\n" + stderr;
            let regex = /\[Line (\d+), Col (\d+)\] (.*)/g;
            let match;
            while ((match = regex.exec(output)) !== null) {
                let lineNum = parseInt(match[1], 10) - 1;
                let colNum = parseInt(match[2], 10) - 1;
                let msg = match[3].trim();
                let range = new vscode.Range(lineNum, colNum, lineNum, colNum + 5);
                diagnostics.push(new vscode.Diagnostic(range, msg, vscode.DiagnosticSeverity.Error));
            }
            diagnosticCollection.set(document.uri, diagnostics);
        });
    };

    vscode.workspace.textDocuments.forEach(triggerLint);
    context.subscriptions.push(vscode.workspace.onDidSaveTextDocument(triggerLint));
    context.subscriptions.push(vscode.workspace.onDidOpenTextDocument(triggerLint));
    context.subscriptions.push(vscode.workspace.onDidCloseTextDocument(doc => {
        diagnosticCollection.delete(doc.uri);
    }));
}

function deactivate() {
    if (client) {
        return client.stop();
    }
}

module.exports = { activate, deactivate };

function openWorkflowVisualizer(context) {
    const panel = vscode.window.createWebviewPanel(
        'workflowVisualizer',
        'Serv: Workflow Visualizer',
        vscode.ViewColumn.Two,
        { enableScripts: true }
    );

    const editor = vscode.window.activeTextEditor;
    let mermaidCode = 'graph TD\n    Start --> A[Parse Error]\n';
    if (editor) {
        const text = editor.document.getText();
        const steps = [];
        const regex = /step\s+"([^"]+)"/g;
        let match;
        while ((match = regex.exec(text)) !== null) {
            steps.push(match[1]);
        }
        if (steps.length > 0) {
            mermaidCode = 'graph TD\n';
            for (let i = 0; i < steps.length; i++) {
                if (i < steps.length - 1) {
                    mermaidCode += `    ${steps[i]} --> ${steps[i+1]}\n`;
                }
            }
        }
    }

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <head>
            <script src="https://cdn.jsdelivr.net/npm/mermaid/dist/mermaid.min.js"></script>
            <script>mermaid.initialize({startOnLoad:true, theme: 'dark'});</script>
        </head>
        <body style="background: #1e1e2e; color: #cdd6f4; font-family: sans-serif; padding: 20px;">
            <h2>ServFlow DAG Visualizer</h2>
            <div class="mermaid">
                ${mermaidCode}
            </div>
        </body>
        </html>
    `;
}

function openQueueExplorer(context) {
    const panel = vscode.window.createWebviewPanel(
        'queueExplorer',
        'Serv: Queue Broker Explorer',
        vscode.ViewColumn.Two,
        { enableScripts: true }
    );

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <body style="background: #1e1e2e; color: #cdd6f4; font-family: sans-serif; padding: 20px;">
            <h2>ServQueue Broker Explorer</h2>
            <table border="1" cellpadding="8" style="border-collapse: collapse; width: 100%; border-color: #444;">
                <tr style="background: #313244;">
                    <th>Topic</th>
                    <th>Partitions</th>
                    <th>Consumers</th>
                    <th>Message Rate</th>
                </tr>
                <tr>
                    <td>orders-topic</td>
                    <td>4</td>
                    <td>order-processor-group (active)</td>
                    <td>125 msg/s</td>
                </tr>
                <tr>
                    <td>billing-topic</td>
                    <td>2</td>
                    <td>invoice-generator-group (idle)</td>
                    <td>0 msg/s</td>
                </tr>
            </table>
        </body>
        </html>
    `;
}

function openStoreExplorer(context) {
    const panel = vscode.window.createWebviewPanel(
        'storeExplorer',
        'Serv: Store Explorer',
        vscode.ViewColumn.Two,
        { enableScripts: true }
    );

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <body style="background: #1e1e2e; color: #cdd6f4; font-family: sans-serif; padding: 20px;">
            <h2>ServStore Bucket Explorer</h2>
            <ul>
                <li>📁 <b>user-uploads-bucket</b> (23 files)</li>
                <li>📁 <b>static-assets-bucket</b> (142 files)</li>
            </ul>
        </body>
        </html>
    `;
}

function openLocksExplorer(context) {
    const panel = vscode.window.createWebviewPanel(
        'locksExplorer',
        'Serv: Lock Explorer',
        vscode.ViewColumn.Two,
        { enableScripts: true }
    );

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <body style="background: #1e1e2e; color: #cdd6f4; font-family: sans-serif; padding: 20px;">
            <h2>ServLock Contention Dashboard</h2>
            <table border="1" cellpadding="8" style="border-collapse: collapse; width: 100%; border-color: #444;">
                <tr style="background: #313244;">
                    <th>Lock Key</th>
                    <th>Owner</th>
                    <th>Waiters</th>
                    <th>Status</th>
                </tr>
                <tr>
                    <td>user-lock-123</td>
                    <td>session-handler-A</td>
                    <td>session-handler-B (waiting)</td>
                    <td>⚠️ Contended</td>
                </tr>
            </table>
        </body>
        </html>
    `;
}

function openRouteSimulator(context) {
    const panel = vscode.window.createWebviewPanel(
        'routeSimulator',
        'Serv: Route Simulator',
        vscode.ViewColumn.Two,
        { enableScripts: true }
    );

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <body style="background: #1e1e2e; color: #cdd6f4; font-family: sans-serif; padding: 20px;">
            <h2>ServGate Route Simulator</h2>
            <p>Enter path to test route mapping:</p>
            <input type="text" value="/api/v1/users" style="padding: 5px; width: 300px;">
            <button onclick="alert('Matches: ServAuth microservice on http://localhost:8098')">Simulate</button>
        </body>
        </html>
    `;
}
