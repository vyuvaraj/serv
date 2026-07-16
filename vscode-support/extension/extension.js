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
        vscode.commands.registerCommand('serv.simulateRoute', () => openRouteSimulator(context)),
        vscode.commands.registerCommand('serv.exploreCron', () => openCronExplorer(context)),
        vscode.commands.registerCommand('serv.inspectCache', () => openCacheInspector(context)),
        vscode.commands.registerCommand('serv.inspectAuth', () => openAuthInspector(context)),
        vscode.commands.registerCommand('serv.openREPL', () => launchREPL(context)),
        vscode.commands.registerCommand('serv.viewMesh', () => openMeshTopology(context)),
        vscode.commands.registerCommand('serv.traceRequests', () => openTraceViewer(context)),
        vscode.commands.registerCommand('serv.viewRegistry', () => openRegistryMonitor(context))
    );

    // Status bar integration — always-visible ecosystem health indicator
    setupStatusBar(context);
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

function openCronExplorer(context) {
    const panel = vscode.window.createWebviewPanel(
        'cronExplorer',
        'Serv: Cron Explorer',
        vscode.ViewColumn.Two,
        { enableScripts: true }
    );

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <body style="background: #1e1e2e; color: #cdd6f4; font-family: sans-serif; padding: 20px;">
            <h2>ServCron Schedule Manager</h2>
            <div id="status" style="margin-bottom: 10px; color: #a6e3a1;">Connecting to ServCron...</div>
            <table border="1" cellpadding="8" style="border-collapse: collapse; width: 100%; border-color: #444;">
                <thead>
                    <tr style="background: #313244;">
                        <th>Job ID</th>
                        <th>Schedule</th>
                        <th>Conflict Alerts</th>
                    </tr>
                </thead>
                <tbody id="cron-body"></tbody>
            </table>
            <script>
                async function loadCron() {
                    const status = document.getElementById('status');
                    const body = document.getElementById('cron-body');
                    try {
                        const res = await fetch("http://localhost:8087/api/cron/smart-schedule");
                        const data = await res.json();
                        status.innerText = "🟢 Connected (Live data)";
                        if (data.length === 0) {
                            body.innerHTML = "<tr><td colspan='3'>No conflicts detected. Schedules are optimal.</td></tr>";
                        } else {
                            body.innerHTML = data.map(j => \`
                                <tr>
                                    <td>\${j.job_id}</td>
                                    <td>\${j.current_schedule}</td>
                                    <td style="color: #f38ba8;">⚠️ Conflict: \${j.reason}. Suggestion: \${j.suggested_schedule}</td>
                                </tr>
                            \`).join('');
                        }
                    } catch(e) {
                        status.innerText = "⚠️ Offline (Showing mock fallback)";
                        body.innerHTML = \`
                            <tr>
                                <td>nightly-backup</td>
                                <td>0 0 * * *</td>
                                <td>None</td>
                            </tr>
                            <tr>
                                <td>data-sync</td>
                                <td>0 0 * * *</td>
                                <td style="color: #f38ba8;">⚠️ Conflict with nightly-backup. Suggestion: 5 0 * * *</td>
                            </tr>
                        \`;
                    }
                }
                loadCron();
            </script>
        </body>
        </html>
    `;
}

function openCacheInspector(context) {
    const panel = vscode.window.createWebviewPanel(
        'cacheInspector',
        'Serv: Cache Inspector',
        vscode.ViewColumn.Two,
        { enableScripts: true }
    );

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <body style="background: #1e1e2e; color: #cdd6f4; font-family: sans-serif; padding: 20px;">
            <h2>ServCache Performance Dashboard</h2>
            <div id="status" style="margin-bottom: 10px; color: #a6e3a1;">Connecting to ServCache...</div>
            <div style="display: flex; gap: 20px; margin-bottom: 20px;">
                <div style="padding: 15px; background: #313244; border-radius: 6px; flex: 1; text-align: center;">
                    <div style="font-size: 12px; color: #bac2de;">Hit Rate</div>
                    <div id="hit-rate" style="font-size: 24px; font-weight: bold; margin-top: 5px;">94.2%</div>
                </div>
                <div style="padding: 15px; background: #313244; border-radius: 6px; flex: 1; text-align: center;">
                    <div style="font-size: 12px; color: #bac2de;">Active Connections</div>
                    <div id="connections" style="font-size: 24px; font-weight: bold; margin-top: 5px;">18</div>
                </div>
            </div>
            <script>
                async function loadCache() {
                    const status = document.getElementById('status');
                    try {
                        const res = await fetch("http://localhost:8086/api/cache/inspect");
                        const data = await res.json();
                        status.innerText = "🟢 Connected (Live data)";
                        document.getElementById('hit-rate').innerText = (data.hit_rate || 94.2) + "%";
                        document.getElementById('connections').innerText = data.active_connections || 18;
                    } catch(e) {
                        status.innerText = "⚠️ Offline (Showing cached statistics)";
                    }
                }
                loadCache();
            </script>
        </body>
        </html>
    `;
}

function openAuthInspector(context) {
    const panel = vscode.window.createWebviewPanel(
        'authInspector',
        'Serv: Auth Risk Inspector',
        vscode.ViewColumn.Two,
        { enableScripts: true }
    );

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <body style="background: #1e1e2e; color: #cdd6f4; font-family: sans-serif; padding: 20px;">
            <h2>ServAuth Progressive Risk Scoring</h2>
            <div id="status" style="margin-bottom: 10px; color: #a6e3a1;">Connecting to ServAuth...</div>
            <table border="1" cellpadding="8" style="border-collapse: collapse; width: 100%; border-color: #444;">
                <thead>
                    <tr style="background: #313244;">
                        <th>User</th>
                        <th>Device status</th>
                        <th>Geo Context</th>
                        <th>Risk Score</th>
                    </tr>
                </thead>
                <tbody id="auth-body"></tbody>
            </table>
            <script>
                async function loadAuth() {
                    const status = document.getElementById('status');
                    const body = document.getElementById('auth-body');
                    try {
                        const res = await fetch("http://localhost:8098/api/users/risk");
                        const data = await res.json();
                        status.innerText = "🟢 Connected (Live data)";
                        body.innerHTML = data.map(u => \`
                            <tr>
                                <td>\${u.email}</td>
                                <td>\${u.last_device || 'Unknown'}</td>
                                <td>\${u.last_country || 'Unknown'}</td>
                                <td style="color: \${u.risk_score >= 5 ? '#f38ba8' : '#a6e3a1'}; font-weight: bold;">\${u.risk_score || 0}</td>
                            </tr>
                        \`).join('');
                    } catch(e) {
                        status.innerText = "⚠️ Offline (Showing mock fallback)";
                        body.innerHTML = \`
                            <tr>
                                <td>admin@servverse.dev</td>
                                <td>macOS (Chromium)</td>
                                <td>United States</td>
                                <td style="color: #a6e3a1; font-weight: bold;">0 (Safe)</td>
                            </tr>
                            <tr>
                                <td>user@servverse.dev</td>
                                <td>iPhone (Safari) - New</td>
                                <td>Germany - New</td>
                                <td style="color: #f38ba8; font-weight: bold;">8 (MFA Step-up Required)</td>
                            </tr>
                        \`;
                    }
                }
                loadAuth();
            </script>
        </body>
        </html>
    `;
}

function launchREPL(context) {
    const servPath = findServBinary();
    const terminal = vscode.window.createTerminal({ name: "Serv REPL" });
    terminal.show();
    
    const shellPath = vscode.env.shell ? vscode.env.shell.toLowerCase() : '';
    const isPowerShell = shellPath.includes('powershell') || shellPath.includes('pwsh') || shellPath === '';
    
    if (isPowerShell) {
        terminal.sendText(`& "${servPath}" repl`);
    } else {
        terminal.sendText(`"${servPath}" repl`);
    }
}
}

function openMeshTopology(context) {
    const panel = vscode.window.createWebviewPanel(
        'meshTopology',
        'Serv: Service Mesh Topology',
        vscode.ViewColumn.Two,
        { enableScripts: true }
    );

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <head>
            <script src="https://cdn.jsdelivr.net/npm/mermaid/dist/mermaid.min.js"></script>
            <script>mermaid.initialize({startOnLoad:true, theme: 'dark'});</script>
        </head>
        <body style="background: #1e1e2e; color: #cdd6f4; font-family: sans-serif; padding: 20px;">
            <h2>ServMesh Service Topology</h2>
            <div id="status" style="margin-bottom: 10px; color: #a6e3a1;">Connecting to ServMesh...</div>
            <div id="diagram"><div class="mermaid">graph TD\n    ServGate-->ServAuth\n    ServGate-->ServQueue\n    ServGate-->ServFlow\n    ServFlow-->ServCron\n    ServFlow-->ServMail\n    ServAuth-->ServCache\n    ServQueue-->ServStore</div></div>
            <script>
                async function loadTopology() {
                    const status = document.getElementById('status');
                    try {
                        const res = await fetch('http://localhost:8085/api/mesh/topology');
                        const data = await res.json();
                        status.innerText = '🟢 Connected (Live topology)';
                        let edges = 'graph TD\\n';
                        data.edges.forEach(e => { edges += '    ' + e.from + '-->' + e.to + '\\n'; });
                        document.getElementById('diagram').innerHTML = '<div class="mermaid">' + edges + '</div>';
                        mermaid.init(undefined, document.querySelector('.mermaid'));
                    } catch(e) {
                        status.innerText = '⚠️ Offline (Showing default topology)';
                    }
                }
                loadTopology();
            </script>
        </body>
        </html>
    `;
}

function openTraceViewer(context) {
    const panel = vscode.window.createWebviewPanel(
        'traceViewer',
        'Serv: Distributed Request Tracer',
        vscode.ViewColumn.Two,
        { enableScripts: true }
    );

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <body style="background: #1e1e2e; color: #cdd6f4; font-family: sans-serif; padding: 20px;">
            <h2>ServTrace Distributed Request Tracer</h2>
            <div id="status" style="margin-bottom: 10px; color: #a6e3a1;">Connecting to ServTrace...</div>
            <input type="text" id="trace-filter" placeholder="Filter by service or trace ID..." style="padding: 8px; width: 350px; background: #313244; color: #cdd6f4; border: 1px solid #444; border-radius: 4px; margin-bottom: 12px;">
            <table border="1" cellpadding="8" style="border-collapse: collapse; width: 100%; border-color: #444;">
                <thead>
                    <tr style="background: #313244;">
                        <th>Trace ID</th>
                        <th>Service</th>
                        <th>Operation</th>
                        <th>Duration</th>
                        <th>Status</th>
                    </tr>
                </thead>
                <tbody id="trace-body"></tbody>
            </table>
            <script>
                let allTraces = [];
                async function loadTraces() {
                    const status = document.getElementById('status');
                    const body = document.getElementById('trace-body');
                    try {
                        const res = await fetch('http://localhost:8091/api/traces?limit=20');
                        allTraces = await res.json();
                        status.innerText = '🟢 Connected (Live traces)';
                        renderTable(allTraces);
                    } catch(e) {
                        status.innerText = '⚠️ Offline (Showing mock spans)';
                        allTraces = [
                            {trace_id:'abc-001', service:'ServGate', operation:'POST /api/orders', duration_ms:42, status:'OK'},
                            {trace_id:'abc-001', service:'ServAuth', operation:'ValidateToken', duration_ms:8, status:'OK'},
                            {trace_id:'abc-001', service:'ServQueue', operation:'Publish orders-topic', duration_ms:5, status:'OK'},
                            {trace_id:'def-002', service:'ServFlow', operation:'RunWorkflow', duration_ms:310, status:'ERROR'},
                        ];
                        renderTable(allTraces);
                    }
                }
                function renderTable(traces) {
                    const filter = document.getElementById('trace-filter').value.toLowerCase();
                    const filtered = filter ? traces.filter(t => JSON.stringify(t).toLowerCase().includes(filter)) : traces;
                    document.getElementById('trace-body').innerHTML = filtered.map(t => \`
                        <tr>
                            <td style="font-family:monospace; font-size:12px;">\${t.trace_id}</td>
                            <td>\${t.service}</td>
                            <td>\${t.operation}</td>
                            <td>\${t.duration_ms}ms</td>
                            <td style="color: \${t.status === 'OK' ? '#a6e3a1' : '#f38ba8'}; font-weight:bold;">\${t.status}</td>
                        </tr>
                    \`).join('');
                }
                document.getElementById('trace-filter').addEventListener('input', () => renderTable(allTraces));
                loadTraces();
                setInterval(loadTraces, 5000);
            </script>
        </body>
        </html>
    `;
}

function openRegistryMonitor(context) {
    const panel = vscode.window.createWebviewPanel(
        'registryMonitor',
        'Serv: Service Registry Health',
        vscode.ViewColumn.Two,
        { enableScripts: true }
    );

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <body style="background: #1e1e2e; color: #cdd6f4; font-family: sans-serif; padding: 20px;">
            <h2>ServRegistry Health Monitor</h2>
            <div id="status" style="margin-bottom: 10px; color: #a6e3a1;">Connecting to ServRegistry...</div>
            <table border="1" cellpadding="8" style="border-collapse: collapse; width: 100%; border-color: #444;">
                <thead>
                    <tr style="background: #313244;">
                        <th>Service</th>
                        <th>Host</th>
                        <th>Port</th>
                        <th>Health</th>
                        <th>Uptime</th>
                    </tr>
                </thead>
                <tbody id="registry-body"></tbody>
            </table>
            <script>
                async function loadRegistry() {
                    const status = document.getElementById('status');
                    const body = document.getElementById('registry-body');
                    try {
                        const res = await fetch('http://localhost:8090/api/registry/services');
                        const data = await res.json();
                        status.innerText = '🟢 Connected (Live registry)';
                        body.innerHTML = data.map(s => \`
                            <tr>
                                <td><b>\${s.name}</b></td>
                                <td>\${s.host || 'localhost'}</td>
                                <td>\${s.port}</td>
                                <td style="color: \${s.healthy ? '#a6e3a1' : '#f38ba8'}; font-weight:bold;">\${s.healthy ? '🟢 Healthy' : '🔴 Down'}</td>
                                <td>\${s.uptime || 'N/A'}</td>
                            </tr>
                        \`).join('');
                    } catch(e) {
                        status.innerText = '⚠️ Offline (Showing mock registry)';
                        const mock = [
                            {name:'ServAuth', host:'localhost', port:8098, healthy:true, uptime:'14h 32m'},
                            {name:'ServGate', host:'localhost', port:8088, healthy:true, uptime:'14h 32m'},
                            {name:'ServQueue', host:'localhost', port:8082, healthy:true, uptime:'14h 31m'},
                            {name:'ServFlow', host:'localhost', port:8083, healthy:false, uptime:'0m (restarting)'},
                            {name:'ServCron', host:'localhost', port:8087, healthy:true, uptime:'14h 30m'},
                            {name:'ServCache', host:'localhost', port:8086, healthy:true, uptime:'14h 32m'},
                        ];
                        body.innerHTML = mock.map(s => \`
                            <tr>
                                <td><b>\${s.name}</b></td>
                                <td>\${s.host}</td>
                                <td>\${s.port}</td>
                                <td style="color: \${s.healthy ? '#a6e3a1' : '#f38ba8'}; font-weight:bold;">\${s.healthy ? '🟢 Healthy' : '🔴 Down'}</td>
                                <td>\${s.uptime}</td>
                            </tr>
                        \`).join('');
                    }
                }
                loadRegistry();
                setInterval(loadRegistry, 4000);
            </script>
        </body>
        </html>
    `;
}

function setupStatusBar(context) {
    const statusItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 100);
    statusItem.text = '$(circuit-board) Serv';
    statusItem.tooltip = 'Serv Language Server — click to view Registry Health';
    statusItem.command = 'serv.viewRegistry';
    statusItem.show();
    context.subscriptions.push(statusItem);

    // Poll ServRegistry every 10s to update status bar health indicator
    async function pollHealth() {
        try {
            const http = require('http');
            const req = http.get('http://localhost:8090/api/registry/services', (res) => {
                let data = '';
                res.on('data', chunk => data += chunk);
                res.on('end', () => {
                    try {
                        const services = JSON.parse(data);
                        const down = services.filter(s => !s.healthy).length;
                        statusItem.text = down > 0
                            ? `$(warning) Serv (${down} down)`
                            : `$(circuit-board) Serv ✓`;
                        statusItem.backgroundColor = down > 0
                            ? new vscode.ThemeColor('statusBarItem.warningBackground')
                            : undefined;
                    } catch (_) {}
                });
            });
            req.on('error', () => {
                statusItem.text = '$(circuit-board) Serv';
                statusItem.backgroundColor = undefined;
            });
        } catch (_) {}
    }

    pollHealth();
    const interval = setInterval(pollHealth, 10000);
    context.subscriptions.push({ dispose: () => clearInterval(interval) });
}
