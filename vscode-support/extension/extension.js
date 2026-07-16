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
    let mermaidCode = 'graph TD\n    Start --> A[No steps found]\n';
    if (editor) {
        const text = editor.document.getText();
        const steps = [];
        const stepRegex = /step\s+"([^"]+)"\s*(?:\{([^}]+)\})?/g;
        let match;
        while ((match = stepRegex.exec(text)) !== null) {
            const stepName = match[1];
            let deps = [];
            if (match[2]) {
                const depMatch = /depends_on\s*=\s*(?:\[([^\]]+)\]|"([^"]+)")/.exec(match[2]);
                if (depMatch) {
                    const depContent = depMatch[1] || depMatch[2];
                    deps = depContent.split(',').map(d => d.replace(/["\s]/g, '')).filter(d => d);
                }
            }
            steps.push({ name: stepName, deps });
        }
        if (steps.length > 0) {
            mermaidCode = 'graph TD\n';
            steps.forEach(s => {
                if (s.deps.length === 0) {
                    mermaidCode += `    Start --> ${s.name}\n`;
                } else {
                    s.deps.forEach(dep => {
                        mermaidCode += `    ${dep} --> ${s.name}\n`;
                    });
                }
            });
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
            <div id="status" style="margin-bottom: 10px; color: #a6e3a1;">Connecting to ServQueue...</div>
            <table border="1" cellpadding="8" style="border-collapse: collapse; width: 100%; border-color: #444;">
                <thead>
                    <tr style="background: #313244;">
                        <th>Topic</th>
                        <th>Partitions</th>
                        <th>Consumers</th>
                    </tr>
                </thead>
                <tbody id="topics-body"></tbody>
            </table>
            <script>
                async function loadTopics() {
                    const status = document.getElementById('status');
                    const body = document.getElementById('topics-body');
                    try {
                        const res = await fetch("http://localhost:8082/api/topics");
                        const data = await res.json();
                        status.innerText = "🟢 Connected (Live data)";
                        body.innerHTML = data.map(t => \`
                            <tr>
                                <td>\${t.name || t.topic}</td>
                                <td>\${t.partitions || 1}</td>
                                <td>\${t.consumers ? t.consumers.join(', ') : 'None'}</td>
                            </tr>
                        \`).join('');
                    } catch(e) {
                        status.innerText = "⚠️ Offline (Showing mock fallback)";
                        body.innerHTML = \`
                            <tr>
                                <td>orders-topic</td>
                                <td>4</td>
                                <td>order-processor-group</td>
                            </tr>
                            <tr>
                                <td>billing-topic</td>
                                <td>2</td>
                                <td>invoice-generator-group</td>
                            </tr>
                        \`;
                    }
                }
                loadTopics();
                setInterval(loadTopics, 3000);
            </script>
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
            <div id="status" style="margin-bottom: 10px; color: #a6e3a1;">Connecting to ServStore...</div>
            <ul id="buckets-list"></ul>
            <script>
                async function loadBuckets() {
                    const status = document.getElementById('status');
                    const list = document.getElementById('buckets-list');
                    try {
                        const res = await fetch("http://localhost:8081/api/buckets");
                        const data = await res.json();
                        status.innerText = "🟢 Connected (Live data)";
                        list.innerHTML = data.map(b => \`<li>📁 <b>\${b.name || b}</b></li>\`).join('');
                    } catch(e) {
                        status.innerText = "⚠️ Offline (Showing mock fallback)";
                        list.innerHTML = \`
                            <li>📁 <b>user-uploads-bucket</b></li>
                            <li>📁 <b>static-assets-bucket</b></li>
                        \`;
                    }
                }
                loadBuckets();
            </script>
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
            <div id="status" style="margin-bottom: 10px; color: #a6e3a1;">Connecting to ServLock...</div>
            <table border="1" cellpadding="8" style="border-collapse: collapse; width: 100%; border-color: #444;">
                <thead>
                    <tr style="background: #313244;">
                        <th>Lock Key</th>
                        <th>Owner</th>
                        <th>Waiters</th>
                    </tr>
                </thead>
                <tbody id="locks-body"></tbody>
            </table>
            <script>
                async function loadLocks() {
                    const status = document.getElementById('status');
                    const body = document.getElementById('locks-body');
                    try {
                        const res = await fetch("http://localhost:8089/api/locks/observability");
                        const data = await res.json();
                        status.innerText = "🟢 Connected (Live data)";
                        body.innerHTML = data.map(l => \`
                            <tr>
                                <td>\${l.key}</td>
                                <td>\${l.owner}</td>
                                <td>\${l.waiters ? l.waiters.join(', ') : 'None'}</td>
                            </tr>
                        \`).join('');
                    } catch(e) {
                        status.innerText = "⚠️ Offline (Showing mock fallback)";
                        body.innerHTML = \`
                            <tr>
                                <td>user-lock-123</td>
                                <td>session-handler-A</td>
                                <td>session-handler-B (waiting)</td>
                            </tr>
                        \`;
                    }
                }
                loadLocks();
                setInterval(loadLocks, 2000);
            </script>
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

    const workspaceFolders = vscode.workspace.workspaceFolders;
    let gateConfig = "{}";
    if (workspaceFolders) {
        const root = workspaceFolders[0].uri.fsPath;
        const configPath = path.join(root, 'ServGate', 'config.json');
        if (fs.existsSync(configPath)) {
            gateConfig = fs.readFileSync(configPath, 'utf8');
        }
    }

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <body style="background: #1e1e2e; color: #cdd6f4; font-family: sans-serif; padding: 20px;">
            <h2>ServGate Route Simulator</h2>
            <p>Enter path to test route mapping:</p>
            <input type="text" id="route-path" value="/api/v1/users" style="padding: 8px; width: 300px; background: #313244; color: #cdd6f4; border: 1px solid #444; border-radius: 4px;">
            <button onclick="simulate()" style="padding: 8px 16px; background: #89b4fa; color: #11111b; border: none; border-radius: 4px; cursor: pointer; font-weight: bold;">Simulate</button>
            <div id="result" style="margin-top: 20px; font-family: monospace; white-space: pre-wrap; padding: 10px; background: #313244; border-radius: 4px;"></div>
            <script>
                const config = ${gateConfig};
                function simulate() {
                    const pathVal = document.getElementById('route-path').value;
                    const result = document.getElementById('result');
                    if (!config || !config.routes) {
                        result.innerHTML = "No configuration found at ServGate/config.json";
                        return;
                    }
                    const route = config.routes.find(r => pathVal.startsWith(r.prefix));
                    if (route) {
                        result.innerHTML = \`✅ Route Match Found:\\n\\nPrefix: \${route.prefix}\\nTarget: \${route.target || 'None'}\\nRate Limit: \${route.rate_limit_rpm || 'Unlimited'} RPM\\nPii Redaction: \${route.pii_redact ? 'Enabled' : 'Disabled'}\`;
                    } else {
                        result.innerHTML = "❌ No matching route prefix in gateway config";
                    }
                }
                simulate();
            </script>
        </body>
        </html>
    `;
}
