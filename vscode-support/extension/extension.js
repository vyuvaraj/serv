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
        vscode.commands.registerCommand('serv.viewRegistry', () => openRegistryMonitor(context)),
        vscode.commands.registerCommand('serv.runBench', () => runBenchPanel(context)),
        vscode.commands.registerCommand('serv.viewDeployments', () => openDeploymentsPanel(context)),
        vscode.commands.registerCommand('serv.inspectPool', () => openPoolInspector(context)),
        vscode.commands.registerCommand('serv.inspectMail', () => openMailInspector(context)),
        vscode.commands.registerCommand('serv.refreshTests', () => testExplorerProvider.refresh()),
        vscode.commands.registerCommand('serv.runTestsWithGutter', () => runTestsWithGutter(gutterManager)),
        vscode.commands.registerCommand('serv.clearTestDecorations', () => gutterManager.clearAll()),
        vscode.commands.registerCommand('serv.viewTunnels', () => openTunnelViewer(context)),
        vscode.commands.registerCommand('serv.refreshServices', () => servicesPanelProvider.refresh()),
        vscode.commands.registerCommand('serv.organizeImports', () => organizeImports())
    );

    // Status bar integration
    setupStatusBar(context);

    // Test Explorer sidebar tree view
    const testExplorerProvider = new ServTestExplorerProvider();
    vscode.window.registerTreeDataProvider('serv-test-explorer', testExplorerProvider);
    vscode.workspace.onDidSaveTextDocument(doc => {
        if (doc.languageId === 'serv') testExplorerProvider.refresh();
    });

    // CD.113 — Inlay type hints
    const config = vscode.workspace.getConfiguration('serv');
    if (config.get('enableInlayHints', true)) {
        context.subscriptions.push(
            vscode.languages.registerInlayHintsProvider(
                { language: 'serv' },
                new ServInlayHintsProvider()
            )
        );
    }

    // CD.115 — Test gutter decorations manager
    const gutterManager = new ServTestGutterManager(context);
    vscode.window.onDidChangeActiveTextEditor(editor => {
        if (editor && editor.document.languageId === 'serv') {
            gutterManager.restoreForDocument(editor.document);
        }
    });

    // CD.116 — Import auto-organization (completion + code actions)
    context.subscriptions.push(
        vscode.languages.registerCompletionItemProvider(
            { language: 'serv' },
            new ServImportCompletionProvider(),
            ' '  // trigger on space after "use"
        ),
        vscode.languages.registerCodeActionsProvider(
            { language: 'serv' },
            new ServImportCodeActionProvider(),
            { providedCodeActionKinds: [vscode.CodeActionKind.QuickFix] }
        )
    );

    // CD.119 — ServVerse Services Activity Bar panel
    const servicesPanelProvider = new ServServicesPanelProvider();
    vscode.window.registerTreeDataProvider('serv-services-panel', servicesPanelProvider);
    servicesPanelProvider.startPolling(context);
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

// ─── v3.0.4: Test Explorer Tree View ─────────────────────────────────────────

class ServTestExplorerProvider {
    constructor() {
        this._onDidChangeTreeData = new vscode.EventEmitter();
        this.onDidChangeTreeData = this._onDidChangeTreeData.event;
    }

    refresh() { this._onDidChangeTreeData.fire(); }

    getTreeItem(element) { return element; }

    getChildren(element) {
        if (!element) {
            return this._getSrvFiles();
        }
        return this._getTestsInFile(element.resourceUri.fsPath);
    }

    _getSrvFiles() {
        const workspaceFolders = vscode.workspace.workspaceFolders;
        if (!workspaceFolders) return [];
        const results = [];
        for (const folder of workspaceFolders) {
            const files = this._findSrvFiles(folder.uri.fsPath);
            for (const file of files) {
                const tests = this._parseTests(file);
                if (tests.length > 0) {
                    const item = new vscode.TreeItem(
                        vscode.Uri.file(file),
                        vscode.TreeItemCollapsibleState.Collapsed
                    );
                    item.contextValue = 'srvFile';
                    item.iconPath = new vscode.ThemeIcon('file-code');
                    item.description = `${tests.length} test${tests.length > 1 ? 's' : ''}`;
                    results.push(item);
                }
            }
        }
        return results;
    }

    _getTestsInFile(filePath) {
        const tests = this._parseTests(filePath);
        return tests.map(name => {
            const item = new vscode.TreeItem(name, vscode.TreeItemCollapsibleState.None);
            item.iconPath = new vscode.ThemeIcon('beaker');
            item.contextValue = 'srvTest';
            item.command = {
                command: 'vscode.open',
                title: 'Open File',
                arguments: [vscode.Uri.file(filePath)]
            };
            return item;
        });
    }

    _parseTests(filePath) {
        try {
            const content = fs.readFileSync(filePath, 'utf8');
            const matches = [];
            const regex = /test\s+"([^"]+)"/g;
            let match;
            while ((match = regex.exec(content)) !== null) {
                matches.push(match[1]);
            }
            return matches;
        } catch (_) { return []; }
    }

    _findSrvFiles(dir) {
        const results = [];
        try {
            const entries = fs.readdirSync(dir, { withFileTypes: true });
            for (const entry of entries) {
                if (entry.name === 'node_modules' || entry.name === 'vendor' || entry.name.startsWith('.')) continue;
                const fullPath = path.join(dir, entry.name);
                if (entry.isDirectory()) {
                    results.push(...this._findSrvFiles(fullPath));
                } else if (entry.name.endsWith('.srv')) {
                    results.push(fullPath);
                }
            }
        } catch (_) {}
        return results;
    }
}

// ─── v3.0.4: Bench Result Panel ───────────────────────────────────────────────

function runBenchPanel(context) {
    const editor = vscode.window.activeTextEditor;
    const filePath = editor ? editor.document.fileName : null;
    const fileName = filePath ? path.basename(filePath) : 'current file';

    // Launch benchmark in terminal
    const servPath = findServBinary();
    const terminal = vscode.window.createTerminal({ name: 'Serv Bench' });
    terminal.show();
    if (filePath) {
        const isPowerShell = (vscode.env.shell || '').toLowerCase().includes('powershell');
        terminal.sendText(isPowerShell
            ? `& "${servPath}" bench "${filePath}"`
            : `"${servPath}" bench "${filePath}"`);
    }

    // Also open a results webview panel
    const panel = vscode.window.createWebviewPanel(
        'benchResults',
        `Serv: Bench — ${fileName}`,
        vscode.ViewColumn.Two,
        { enableScripts: true }
    );

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <body style="background: #1e1e2e; color: #cdd6f4; font-family: sans-serif; padding: 20px;">
            <h2>serv bench — ${fileName}</h2>
            <p style="color: #a6e3a1;">Running benchmark in terminal... Results will appear here if served via API.</p>
            <div style="display:flex; gap:16px; margin-bottom:20px;">
                <div style="padding:15px; background:#313244; border-radius:6px; flex:1; text-align:center;">
                    <div style="font-size:11px; color:#bac2de; margin-bottom:4px;">Throughput</div>
                    <div id="throughput" style="font-size:26px; font-weight:bold; color:#89b4fa;">—</div>
                </div>
                <div style="padding:15px; background:#313244; border-radius:6px; flex:1; text-align:center;">
                    <div style="font-size:11px; color:#bac2de; margin-bottom:4px;">p99 Latency</div>
                    <div id="p99" style="font-size:26px; font-weight:bold; color:#a6e3a1;">—</div>
                </div>
                <div style="padding:15px; background:#313244; border-radius:6px; flex:1; text-align:center;">
                    <div style="font-size:11px; color:#bac2de; margin-bottom:4px;">Error Rate</div>
                    <div id="err" style="font-size:26px; font-weight:bold; color:#f38ba8;">—</div>
                </div>
            </div>
            <table border="1" cellpadding="8" style="border-collapse:collapse; width:100%; border-color:#444;">
                <thead><tr style="background:#313244;"><th>Route</th><th>Req/s</th><th>p50</th><th>p99</th><th>Errors</th></tr></thead>
                <tbody id="bench-body"></tbody>
            </table>
            <script>
                async function loadBench() {
                    try {
                        const res = await fetch("http://localhost:9000/api/bench/results");
                        const data = await res.json();
                        document.getElementById('throughput').innerText = (data.total_rps || 0) + ' req/s';
                        document.getElementById('p99').innerText = (data.p99_ms || 0) + 'ms';
                        document.getElementById('err').innerText = (data.error_rate || 0) + '%';
                        document.getElementById('bench-body').innerHTML = (data.routes || []).map(r => \`
                            <tr>
                                <td style="font-family:monospace;">\${r.path}</td>
                                <td>\${r.rps}</td>
                                <td>\${r.p50_ms}ms</td>
                                <td>\${r.p99_ms}ms</td>
                                <td style="color:\${r.errors > 0 ? '#f38ba8' : '#a6e3a1'}">\${r.errors}</td>
                            </tr>
                        \`).join('');
                    } catch(e) {
                        // Show mock results if no bench API running
                        document.getElementById('throughput').innerText = '4,820 req/s';
                        document.getElementById('p99').innerText = '12ms';
                        document.getElementById('err').innerText = '0.01%';
                        document.getElementById('bench-body').innerHTML = \`
                            <tr><td style="font-family:monospace;">GET /api/users</td><td>2,410</td><td>3ms</td><td>8ms</td><td style="color:#a6e3a1">0</td></tr>
                            <tr><td style="font-family:monospace;">POST /api/orders</td><td>1,290</td><td>6ms</td><td>12ms</td><td style="color:#a6e3a1">0</td></tr>
                            <tr><td style="font-family:monospace;">GET /api/products</td><td>1,120</td><td>2ms</td><td>5ms</td><td style="color:#a6e3a1">0</td></tr>
                        \`;
                    }
                }
                loadBench();
            </script>
        </body>
        </html>
    `;
}

// ─── v3.0.4: Cloud Deployments Panel ─────────────────────────────────────────

function openDeploymentsPanel(context) {
    const panel = vscode.window.createWebviewPanel(
        'deploymentsPanel',
        'Serv: Cloud Deployments',
        vscode.ViewColumn.Two,
        { enableScripts: true }
    );

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <body style="background: #1e1e2e; color: #cdd6f4; font-family: sans-serif; padding: 20px;">
            <h2>ServCloud Branch Deployments</h2>
            <div id="status" style="margin-bottom:10px; color:#a6e3a1;">Connecting to ServCloud...</div>
            <table border="1" cellpadding="8" style="border-collapse:collapse; width:100%; border-color:#444;">
                <thead>
                    <tr style="background:#313244;">
                        <th>Branch</th><th>Service</th><th>Preview URL</th><th>Status</th><th>Deployed At</th>
                    </tr>
                </thead>
                <tbody id="deploy-body"></tbody>
            </table>
            <script>
                async function loadDeployments() {
                    const status = document.getElementById('status');
                    const body = document.getElementById('deploy-body');
                    try {
                        const res = await fetch("http://localhost:8084/api/deployments");
                        const data = await res.json();
                        status.innerText = "🟢 Connected (Live deployments)";
                        body.innerHTML = data.map(d => \`
                            <tr>
                                <td style="font-family:monospace;">\${d.branch}</td>
                                <td>\${d.service}</td>
                                <td><a href="\${d.preview_url}" style="color:#89b4fa;">\${d.preview_url}</a></td>
                                <td style="color:\${d.status === 'running' ? '#a6e3a1' : '#f38ba8'}; font-weight:bold;">\${d.status}</td>
                                <td style="font-size:12px;">\${d.deployed_at || 'N/A'}</td>
                            </tr>
                        \`).join('');
                    } catch(e) {
                        status.innerText = "⚠️ Offline (Showing mock deployments)";
                        body.innerHTML = \`
                            <tr>
                                <td style="font-family:monospace;">feat/order-v2</td>
                                <td>ServGate</td>
                                <td><a href="#" style="color:#89b4fa;">preview-feat-order-v2.local</a></td>
                                <td style="color:#a6e3a1; font-weight:bold;">running</td>
                                <td style="font-size:12px;">2026-07-16 05:47</td>
                            </tr>
                            <tr>
                                <td style="font-family:monospace;">fix/auth-bug</td>
                                <td>ServAuth</td>
                                <td><a href="#" style="color:#89b4fa;">preview-fix-auth-bug.local</a></td>
                                <td style="color:#f9e2af; font-weight:bold;">building</td>
                                <td style="font-size:12px;">2026-07-16 06:01</td>
                            </tr>
                        \`;
                    }
                }
                loadDeployments();
                setInterval(loadDeployments, 5000);
            </script>
        </body>
        </html>
    `;
}

// ─── v3.0.4: Connection Pool Inspector ───────────────────────────────────────

function openPoolInspector(context) {
    const panel = vscode.window.createWebviewPanel(
        'poolInspector',
        'Serv: Connection Pool Inspector',
        vscode.ViewColumn.Two,
        { enableScripts: true }
    );

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <body style="background: #1e1e2e; color: #cdd6f4; font-family: sans-serif; padding: 20px;">
            <h2>ServPool DB Connection Pool</h2>
            <div id="status" style="margin-bottom:10px; color:#a6e3a1;">Connecting to ServPool...</div>
            <div style="display:flex; gap:16px; margin-bottom:20px;">
                <div style="padding:15px; background:#313244; border-radius:6px; flex:1; text-align:center;">
                    <div style="font-size:11px; color:#bac2de; margin-bottom:4px;">Active</div>
                    <div id="active" style="font-size:26px; font-weight:bold; color:#89b4fa;">—</div>
                </div>
                <div style="padding:15px; background:#313244; border-radius:6px; flex:1; text-align:center;">
                    <div style="font-size:11px; color:#bac2de; margin-bottom:4px;">Idle</div>
                    <div id="idle" style="font-size:26px; font-weight:bold; color:#a6e3a1;">—</div>
                </div>
                <div style="padding:15px; background:#313244; border-radius:6px; flex:1; text-align:center;">
                    <div style="font-size:11px; color:#bac2de; margin-bottom:4px;">Max</div>
                    <div id="max" style="font-size:26px; font-weight:bold; color:#cdd6f4;">—</div>
                </div>
            </div>
            <table border="1" cellpadding="8" style="border-collapse:collapse; width:100%; border-color:#444;">
                <thead><tr style="background:#313244;"><th>Pool Name</th><th>DSN</th><th>Active</th><th>Max</th><th>Wait Queue</th></tr></thead>
                <tbody id="pool-body"></tbody>
            </table>
            <script>
                async function loadPool() {
                    const status = document.getElementById('status');
                    const body = document.getElementById('pool-body');
                    try {
                        const res = await fetch("http://localhost:8093/api/pool/stats");
                        const data = await res.json();
                        status.innerText = "🟢 Connected (Live pool stats)";
                        document.getElementById('active').innerText = data.total_active || 0;
                        document.getElementById('idle').innerText = data.total_idle || 0;
                        document.getElementById('max').innerText = data.total_max || 0;
                        body.innerHTML = (data.pools || []).map(p => \`
                            <tr>
                                <td><b>\${p.name}</b></td>
                                <td style="font-size:12px; font-family:monospace;">\${p.dsn || 'N/A'}</td>
                                <td>\${p.active}</td>
                                <td>\${p.max}</td>
                                <td style="color:\${p.wait > 0 ? '#f9e2af' : '#a6e3a1'}">\${p.wait}</td>
                            </tr>
                        \`).join('');
                    } catch(e) {
                        status.innerText = "⚠️ Offline (Showing mock pool stats)";
                        document.getElementById('active').innerText = '8';
                        document.getElementById('idle').innerText = '12';
                        document.getElementById('max').innerText = '25';
                        body.innerHTML = \`
                            <tr><td><b>primary-db</b></td><td style="font-family:monospace; font-size:12px;">postgres://localhost:5432/serv</td><td>8</td><td>20</td><td style="color:#a6e3a1">0</td></tr>
                            <tr><td><b>replica-db</b></td><td style="font-family:monospace; font-size:12px;">postgres://replica:5432/serv</td><td>0</td><td>5</td><td style="color:#a6e3a1">0</td></tr>
                        \`;
                    }
                }
                loadPool();
                setInterval(loadPool, 3000);
            </script>
        </body>
        </html>
    `;
}

// ─── v3.0.4: Mail Queue Inspector ────────────────────────────────────────────

function openMailInspector(context) {
    const panel = vscode.window.createWebviewPanel(
        'mailInspector',
        'Serv: Mail Queue Inspector',
        vscode.ViewColumn.Two,
        { enableScripts: true }
    );

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <body style="background: #1e1e2e; color: #cdd6f4; font-family: sans-serif; padding: 20px;">
            <h2>ServMail Queue Inspector</h2>
            <div id="status" style="margin-bottom:10px; color:#a6e3a1;">Connecting to ServMail...</div>
            <div style="display:flex; gap:16px; margin-bottom:20px;">
                <div style="padding:15px; background:#313244; border-radius:6px; flex:1; text-align:center;">
                    <div style="font-size:11px; color:#bac2de; margin-bottom:4px;">Queued</div>
                    <div id="queued" style="font-size:26px; font-weight:bold; color:#89b4fa;">—</div>
                </div>
                <div style="padding:15px; background:#313244; border-radius:6px; flex:1; text-align:center;">
                    <div style="font-size:11px; color:#bac2de; margin-bottom:4px;">Sent (24h)</div>
                    <div id="sent" style="font-size:26px; font-weight:bold; color:#a6e3a1;">—</div>
                </div>
                <div style="padding:15px; background:#313244; border-radius:6px; flex:1; text-align:center;">
                    <div style="font-size:11px; color:#bac2de; margin-bottom:4px;">Bounced</div>
                    <div id="bounced" style="font-size:26px; font-weight:bold; color:#f38ba8;">—</div>
                </div>
            </div>
            <table border="1" cellpadding="8" style="border-collapse:collapse; width:100%; border-color:#444;">
                <thead><tr style="background:#313244;"><th>To</th><th>Subject</th><th>Template</th><th>Queued At</th><th>Status</th></tr></thead>
                <tbody id="mail-body"></tbody>
            </table>
            <script>
                async function loadMail() {
                    const status = document.getElementById('status');
                    const body = document.getElementById('mail-body');
                    try {
                        const res = await fetch("http://localhost:8092/api/mail/queue");
                        const data = await res.json();
                        status.innerText = "🟢 Connected (Live mail queue)";
                        document.getElementById('queued').innerText = data.queued || 0;
                        document.getElementById('sent').innerText = data.sent_24h || 0;
                        document.getElementById('bounced').innerText = data.bounced || 0;
                        body.innerHTML = (data.items || []).map(m => \`
                            <tr>
                                <td style="font-size:12px;">\${m.to}</td>
                                <td>\${m.subject}</td>
                                <td><code>\${m.template || 'inline'}</code></td>
                                <td style="font-size:12px;">\${m.queued_at}</td>
                                <td style="color:\${m.status === 'sent' ? '#a6e3a1' : m.status === 'bounced' ? '#f38ba8' : '#f9e2af'}; font-weight:bold;">\${m.status}</td>
                            </tr>
                        \`).join('');
                    } catch(e) {
                        status.innerText = "⚠️ Offline (Showing mock queue)";
                        document.getElementById('queued').innerText = '3';
                        document.getElementById('sent').innerText = '147';
                        document.getElementById('bounced').innerText = '2';
                        body.innerHTML = \`
                            <tr><td style="font-size:12px;">alice@example.com</td><td>Order Confirmation</td><td><code>order-confirm</code></td><td style="font-size:12px;">06:01:14</td><td style="color:#f9e2af; font-weight:bold;">queued</td></tr>
                            <tr><td style="font-size:12px;">bob@example.com</td><td>Password Reset</td><td><code>pwd-reset</code></td><td style="font-size:12px;">05:58:02</td><td style="color:#a6e3a1; font-weight:bold;">sent</td></tr>
                            <tr><td style="font-size:12px;">bad@notreal.xyz</td><td>Welcome Email</td><td><code>welcome</code></td><td style="font-size:12px;">05:40:11</td><td style="color:#f38ba8; font-weight:bold;">bounced</td></tr>
                        \`;
                    }
                }
                loadMail();
                setInterval(loadMail, 4000);
            </script>
        </body>
        </html>
    `;
}

// ═══════════════════════════════════════════════════════════════════════════════
// CD.113 — Inlay Type Hints Provider
// Shows inferred return types on fn declarations and variable types on let bindings
// ═══════════════════════════════════════════════════════════════════════════════

class ServInlayHintsProvider {
    provideInlayHints(document, range, token) {
        const hints = [];
        const lines = document.getText().split('\n');

        for (let lineIdx = 0; lineIdx < lines.length; lineIdx++) {
            if (token.isCancellationRequested) break;
            const line = lines[lineIdx];

            // ── fn declarations without explicit return type ───────────────
            // Matches: fn Name(...) { or fn Struct.Method(...) {
            const fnMatch = /^(fn\s+[\w.]+\s*\([^)]*\))\s*\{/.exec(line);
            if (fnMatch) {
                const returnType = this._inferReturnType(lines, lineIdx);
                if (returnType) {
                    const pos = new vscode.Position(lineIdx, fnMatch[1].length);
                    const hint = new vscode.InlayHint(pos, ` \u2192 ${returnType}`);
                    hint.kind = vscode.InlayHintKind.Type;
                    hint.paddingLeft = true;
                    hints.push(hint);
                }
            }

            // ── let bindings without explicit type annotation ─────────────
            // Matches: let name = value  (no colon in assignment)
            const letMatch = /^(\s*let\s+([\w]+))\s*=\s*(.+)/.exec(line);
            if (letMatch && !line.includes(':')) {
                const varName = letMatch[2];
                const valueExpr = letMatch[3].trim();
                const inferredType = this._inferExprType(valueExpr);
                if (inferredType) {
                    const varEnd = line.indexOf(varName) + varName.length;
                    const hint = new vscode.InlayHint(
                        new vscode.Position(lineIdx, varEnd),
                        `: ${inferredType}`
                    );
                    hint.kind = vscode.InlayHintKind.Type;
                    hints.push(hint);
                }
            }
        }
        return hints;
    }

    resolveInlayHint(hint, token) { return hint; }

    _inferReturnType(lines, fnLine) {
        for (let i = fnLine + 1; i < Math.min(fnLine + 40, lines.length); i++) {
            const t = lines[i].trim();
            if (t === '}') break; // end of function body
            if (!t.startsWith('return ')) continue;
            return this._inferExprType(t.slice(7).trim()) || 'any';
        }
        return null;
    }

    _inferExprType(expr) {
        if (!expr) return null;
        const e = expr.trim();
        if (e === 'nil')                       return 'nil';
        if (e === 'true' || e === 'false')     return 'bool';
        if (/^f?"/.test(e))                    return 'string';
        if (/^\d+\.\d+/.test(e))               return 'float';
        if (/^\d+$/.test(e))                   return 'int';
        if (e.startsWith('['))                 return '[]any';
        if (e.startsWith('{') || e === '{}')   return 'map';
        if (e.includes('db.query('))           return 'Result';
        if (e.includes('db.exec('))            return 'Result';
        if (e.includes('cache.get('))          return 'string?';
        if (e.includes('cache.set('))          return 'bool';
        if (e.includes('http.get('))           return 'Response';
        if (e.includes('http.post('))          return 'Response';
        if (e.includes('json.encode('))        return 'string';
        if (e.includes('json.decode('))        return 'map';
        if (e.endsWith('?'))                   return 'any?';
        return null;
    }
}

// ═══════════════════════════════════════════════════════════════════════════════
// CD.115 — Test Gutter Decorations Manager
// Shows 🟢/🔴/🟡 dot icons in the gutter next to each test "..." block
// ═══════════════════════════════════════════════════════════════════════════════

class ServTestGutterManager {
    constructor(context) {
        // Inline SVG data URIs — no external icon files required
        const svg = (fill) => vscode.Uri.parse(
            `data:image/svg+xml;base64,${Buffer.from(
                `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16">` +
                `<circle cx="8" cy="8" r="5" fill="${fill}"/>` +
                `</svg>`
            ).toString('base64')}`
        );

        this._passDecoration = vscode.window.createTextEditorDecorationType({
            gutterIconPath: svg('#a6e3a1'),
            gutterIconSize: 'contain',
            overviewRulerColor: '#a6e3a1',
            overviewRulerLane: vscode.OverviewRulerLane.Left
        });
        this._failDecoration = vscode.window.createTextEditorDecorationType({
            gutterIconPath: svg('#f38ba8'),
            gutterIconSize: 'contain',
            overviewRulerColor: '#f38ba8',
            overviewRulerLane: vscode.OverviewRulerLane.Left
        });
        this._pendingDecoration = vscode.window.createTextEditorDecorationType({
            gutterIconPath: svg('#f9e2af'),
            gutterIconSize: 'contain',
            overviewRulerColor: '#f9e2af',
            overviewRulerLane: vscode.OverviewRulerLane.Left
        });

        // Store last results per file path so we can restore on tab switch
        this._lastResults = new Map(); // filePath -> {pass: Range[], fail: Range[]}

        context.subscriptions.push(
            this._passDecoration,
            this._failDecoration,
            this._pendingDecoration
        );
    }

    markAllPending(document) {
        const editor = vscode.window.visibleTextEditors.find(e => e.document === document);
        if (!editor) return;
        const ranges = this._findTestRanges(document);
        editor.setDecorations(this._passDecoration, []);
        editor.setDecorations(this._failDecoration, []);
        editor.setDecorations(this._pendingDecoration, ranges);
    }

    applyResults(document, testResults) {
        // testResults: Array<{name: string, passed: boolean}>
        const editor = vscode.window.visibleTextEditors.find(e => e.document === document);
        if (!editor) return;

        const lines = document.getText().split('\n');
        const passRanges = [];
        const failRanges = [];

        for (let i = 0; i < lines.length; i++) {
            const m = /test\s+"([^"]+)"/.exec(lines[i]);
            if (!m) continue;
            const result = testResults.find(r => r.name === m[1]);
            if (!result) continue;
            const range = new vscode.Range(i, 0, i, 0);
            if (result.passed) passRanges.push(range);
            else failRanges.push(range);
        }

        editor.setDecorations(this._pendingDecoration, []);
        editor.setDecorations(this._passDecoration, passRanges);
        editor.setDecorations(this._failDecoration, failRanges);

        this._lastResults.set(document.fileName, { pass: passRanges, fail: failRanges });
    }

    restoreForDocument(document) {
        const cached = this._lastResults.get(document.fileName);
        if (!cached) return;
        const editor = vscode.window.visibleTextEditors.find(e => e.document === document);
        if (!editor) return;
        editor.setDecorations(this._passDecoration, cached.pass);
        editor.setDecorations(this._failDecoration, cached.fail);
        editor.setDecorations(this._pendingDecoration, []);
    }

    clearAll() {
        this._lastResults.clear();
        for (const editor of vscode.window.visibleTextEditors) {
            editor.setDecorations(this._passDecoration, []);
            editor.setDecorations(this._failDecoration, []);
            editor.setDecorations(this._pendingDecoration, []);
        }
        vscode.window.showInformationMessage('Serv: Test gutter markers cleared.');
    }

    parseTestOutput(output) {
        // Parse structured test output: "PASS: test name" / "FAIL: test name"
        const results = [];
        for (const line of output.split('\n')) {
            const t = line.trim();
            const passM = /^(?:PASS|ok|✓|pass)[\s:]+(.+)/i.exec(t);
            const failM = /^(?:FAIL|error|✗|fail)[\s:]+(.+)/i.exec(t);
            if (passM) results.push({ name: passM[1].trim(), passed: true });
            if (failM) results.push({ name: failM[1].trim(), passed: false });
        }
        return results;
    }

    _findTestRanges(document) {
        return document.getText().split('\n').reduce((acc, line, i) => {
            if (/test\s+"[^"]+"/.test(line)) acc.push(new vscode.Range(i, 0, i, 0));
            return acc;
        }, []);
    }
}

// ─── CD.115: Test runner with gutter decoration integration ──────────────────

function runTestsWithGutter(gutterManager) {
    const editor = vscode.window.activeTextEditor;
    if (!editor || editor.document.languageId !== 'serv') {
        vscode.window.showWarningMessage('Open a .srv file to run tests with gutter decorations.');
        return;
    }

    const document = editor.document;
    const filePath = document.fileName;
    const servPath = findServBinary();
    const { spawn } = require('child_process');

    // Mark all tests yellow (pending)
    gutterManager.markAllPending(document);

    const outputChannel = vscode.window.createOutputChannel('Serv Tests');
    outputChannel.show(true);
    outputChannel.appendLine(`\u25b6 serv test "${path.basename(filePath)}"`);
    outputChannel.appendLine('─'.repeat(60));

    const proc = spawn(servPath, ['test', filePath], {
        cwd: path.dirname(filePath)
    });

    let fullOutput = '';
    proc.stdout.on('data', chunk => { fullOutput += chunk; outputChannel.append(chunk.toString()); });
    proc.stderr.on('data', chunk => { fullOutput += chunk; outputChannel.append(chunk.toString()); });

    proc.on('close', code => {
        outputChannel.appendLine('─'.repeat(60));

        let results = gutterManager.parseTestOutput(fullOutput);

        // If the runner didn't emit structured PASS/FAIL lines, infer from exit code
        if (results.length === 0) {
            const testNames = document.getText().split('\n').reduce((acc, line) => {
                const m = /test\s+"([^"]+)"/.exec(line);
                if (m) acc.push(m[1]);
                return acc;
            }, []);
            results = testNames.map(name => ({ name, passed: code === 0 }));
        }

        gutterManager.applyResults(document, results);

        const passed = results.filter(r => r.passed).length;
        const failed = results.filter(r => !r.passed).length;
        const summary = `${passed} passed, ${failed} failed`;

        outputChannel.appendLine(
            code === 0
                ? `\u2705 All tests passed (${summary})`
                : `\u274c Tests failed (${summary})`
        );

        vscode.window.showInformationMessage(
            code === 0 ? `\u2705 Serv: ${summary}` : `\u274c Serv: ${summary}`,
            code !== 0 ? 'Show Output' : undefined
        ).then(sel => { if (sel === 'Show Output') outputChannel.show(); });
    });

    proc.on('error', err => {
        outputChannel.appendLine(`\u274c Could not start serv: ${err.message}`);
        gutterManager.clearAll();
    });
}

// ═══════════════════════════════════════════════════════════════════════════════
// CD.120 — ServTunnel Session Viewer
// ═══════════════════════════════════════════════════════════════════════════════

function openTunnelViewer(context) {
    const panel = vscode.window.createWebviewPanel(
        'tunnelViewer',
        'Serv: Tunnel Sessions',
        vscode.ViewColumn.Two,
        { enableScripts: true }
    );

    panel.webview.html = `
        <!DOCTYPE html>
        <html>
        <body style="background:#1e1e2e; color:#cdd6f4; font-family:sans-serif; padding:20px;">
            <h2>ServTunnel — Active Sessions</h2>
            <div id="status" style="margin-bottom:10px; color:#a6e3a1;">Connecting to ServTunnel...</div>
            <div style="display:flex; gap:16px; margin-bottom:20px;">
                <div style="padding:15px; background:#313244; border-radius:6px; flex:1; text-align:center;">
                    <div style="font-size:11px; color:#bac2de; margin-bottom:4px;">Active Tunnels</div>
                    <div id="total" style="font-size:26px; font-weight:bold; color:#89b4fa;">—</div>
                </div>
                <div style="padding:15px; background:#313244; border-radius:6px; flex:1; text-align:center;">
                    <div style="font-size:11px; color:#bac2de; margin-bottom:4px;">Bytes In</div>
                    <div id="bytes-in" style="font-size:26px; font-weight:bold; color:#a6e3a1;">—</div>
                </div>
                <div style="padding:15px; background:#313244; border-radius:6px; flex:1; text-align:center;">
                    <div style="font-size:11px; color:#bac2de; margin-bottom:4px;">Bytes Out</div>
                    <div id="bytes-out" style="font-size:26px; font-weight:bold; color:#f9e2af;">—</div>
                </div>
            </div>
            <table border="1" cellpadding="8" style="border-collapse:collapse; width:100%; border-color:#444;">
                <thead>
                    <tr style="background:#313244;">
                        <th>Tunnel ID</th><th>Client IP</th><th>Target</th><th>Protocol</th><th>Duration</th><th>Status</th>
                    </tr>
                </thead>
                <tbody id="tunnel-body"></tbody>
            </table>
            <script>
                function fmt(bytes) {
                    if (!bytes) return '0 B';
                    if (bytes < 1024) return bytes + ' B';
                    if (bytes < 1048576) return (bytes/1024).toFixed(1) + ' KB';
                    return (bytes/1048576).toFixed(1) + ' MB';
                }
                async function loadTunnels() {
                    const status = document.getElementById('status');
                    const body = document.getElementById('tunnel-body');
                    try {
                        const res = await fetch('http://localhost:8094/api/tunnels');
                        const data = await res.json();
                        status.innerText = '\u{1F7E2} Connected (Live sessions)';
                        document.getElementById('total').innerText = data.length || 0;
                        const totalIn  = data.reduce((s,t) => s + (t.bytes_in  || 0), 0);
                        const totalOut = data.reduce((s,t) => s + (t.bytes_out || 0), 0);
                        document.getElementById('bytes-in').innerText  = fmt(totalIn);
                        document.getElementById('bytes-out').innerText = fmt(totalOut);
                        body.innerHTML = data.map(t => \`
                            <tr>
                                <td style="font-family:monospace;font-size:12px;">\${t.id}</td>
                                <td>\${t.client_ip}</td>
                                <td style="font-family:monospace;">\${t.target}</td>
                                <td>\${t.protocol || 'TCP'}</td>
                                <td>\${t.duration || 'N/A'}</td>
                                <td style="color:\${t.status==='active'?'#a6e3a1':'#f38ba8'};font-weight:bold;">\${t.status}</td>
                            </tr>
                        \`).join('');
                    } catch(e) {
                        status.innerText = '\u26A0\uFE0F Offline (Showing mock sessions)';
                        document.getElementById('total').innerText = '3';
                        document.getElementById('bytes-in').innerText  = '14.2 MB';
                        document.getElementById('bytes-out').innerText = '8.7 MB';
                        body.innerHTML = \`
                            <tr><td style="font-family:monospace;font-size:12px;">tun-a1b2</td><td>203.0.113.42</td><td style="font-family:monospace;">localhost:3000</td><td>TCP</td><td>2h 14m</td><td style="color:#a6e3a1;font-weight:bold;">active</td></tr>
                            <tr><td style="font-family:monospace;font-size:12px;">tun-c3d4</td><td>198.51.100.17</td><td style="font-family:monospace;">localhost:8080</td><td>HTTP</td><td>47m</td><td style="color:#a6e3a1;font-weight:bold;">active</td></tr>
                            <tr><td style="font-family:monospace;font-size:12px;">tun-e5f6</td><td>192.0.2.88</td><td style="font-family:monospace;">localhost:5432</td><td>TCP</td><td>8m</td><td style="color:#f9e2af;font-weight:bold;">idle</td></tr>
                        \`;
                    }
                }
                loadTunnels();
                setInterval(loadTunnels, 4000);
            </script>
        </body>
        </html>
    `;
}

// ═══════════════════════════════════════════════════════════════════════════════
// CD.119 — ServVerse Services Activity Bar Panel
// Live-polling TreeDataProvider showing all 16 services with health status
// ═══════════════════════════════════════════════════════════════════════════════

const SERV_MOCK_SERVICES = [
    { name:'ServAuth',     port:8098, healthy:true,  uptime:'14h 32m' },
    { name:'ServCache',    port:8086, healthy:true,  uptime:'14h 32m' },
    { name:'ServCloud',    port:8084, healthy:true,  uptime:'14h 31m' },
    { name:'ServConsole',  port:8085, healthy:true,  uptime:'14h 31m' },
    { name:'ServCron',     port:8087, healthy:true,  uptime:'14h 30m' },
    { name:'ServDocs',     port:8096, healthy:true,  uptime:'14h 30m' },
    { name:'ServFlow',     port:8083, healthy:false, uptime:'restarting' },
    { name:'ServGate',     port:8088, healthy:true,  uptime:'14h 32m' },
    { name:'ServLock',     port:8089, healthy:true,  uptime:'14h 29m' },
    { name:'ServMail',     port:8092, healthy:true,  uptime:'14h 28m' },
    { name:'ServMesh',     port:8095, healthy:true,  uptime:'14h 32m' },
    { name:'ServPool',     port:8093, healthy:true,  uptime:'14h 32m' },
    { name:'ServQueue',    port:8082, healthy:true,  uptime:'14h 31m' },
    { name:'ServRegistry', port:8090, healthy:true,  uptime:'14h 32m' },
    { name:'ServStore',    port:8081, healthy:true,  uptime:'14h 32m' },
    { name:'ServTrace',    port:8091, healthy:true,  uptime:'14h 32m' },
    { name:'ServTunnel',   port:8094, healthy:true,  uptime:'14h 30m' },
];

class ServServicesPanelProvider {
    constructor() {
        this._onDidChangeTreeData = new vscode.EventEmitter();
        this.onDidChangeTreeData = this._onDidChangeTreeData.event;
        this._services = [];
        this._loading = true;
        this._offline = false;
    }

    refresh() {
        this._poll().catch(() => {});
    }

    getTreeItem(el) { return el; }

    getChildren(el) {
        if (el) return [];

        if (this._loading) {
            const item = new vscode.TreeItem('Connecting to ServRegistry...');
            item.iconPath = new vscode.ThemeIcon('loading~spin');
            return [item];
        }

        const header = new vscode.TreeItem(
            this._offline
                ? `ServVerse  [${this._services.filter(s => s.healthy).length}/${this._services.length} healthy] — offline`
                : `ServVerse  [${this._services.filter(s => s.healthy).length}/${this._services.length} healthy]`
        );
        header.iconPath = new vscode.ThemeIcon('server-environment');
        header.collapsibleState = vscode.TreeItemCollapsibleState.None;
        header.description = this._offline ? 'mock data' : 'live';

        const items = this._services.map(svc => {
            const item = new vscode.TreeItem(svc.name, vscode.TreeItemCollapsibleState.None);
            item.iconPath = new vscode.ThemeIcon(
                svc.healthy ? 'circle-filled' : 'error',
                new vscode.ThemeColor(
                    svc.healthy ? 'testing.iconPassed' : 'testing.iconFailed'
                )
            );
            item.description = svc.healthy
                ? `localhost:${svc.port}  ${svc.uptime}`
                : `localhost:${svc.port}  DOWN`;
            item.tooltip = new vscode.MarkdownString(
                `**${svc.name}**\n\n` +
                `- Port: \`${svc.port}\`\n` +
                `- Status: ${svc.healthy ? '🟢 Healthy' : '🔴 Down'}\n` +
                `- Uptime: ${svc.uptime}`
            );
            item.contextValue = svc.healthy ? 'servHealthy' : 'servDown';
            return item;
        });

        return [header, ...items];
    }

    startPolling(context) {
        this._poll().catch(() => {});
        const interval = setInterval(() => this._poll().catch(() => {}), 6000);
        context.subscriptions.push({ dispose: () => clearInterval(interval) });
    }

    async _poll() {
        try {
            const http = require('http');
            const data = await new Promise((resolve, reject) => {
                const req = http.get('http://localhost:8090/api/registry/services', res => {
                    let body = '';
                    res.on('data', chunk => body += chunk);
                    res.on('end', () => {
                        try { resolve(JSON.parse(body)); }
                        catch (e) { reject(e); }
                    });
                });
                req.setTimeout(3000, () => { req.destroy(); reject(new Error('timeout')); });
                req.on('error', reject);
            });
            this._services = data;
            this._offline = false;
            this._loading = false;
        } catch (_) {
            if (this._loading) {
                // First load — use mock so panel isn't blank
                this._services = SERV_MOCK_SERVICES;
                this._offline = true;
                this._loading = false;
            }
        }
        this._onDidChangeTreeData.fire();
    }
}

// ═══════════════════════════════════════════════════════════════════════════════
// CD.116 — Import Auto-Organization
// CompletionItemProvider: "use <Tab>" shows all stdlib modules
// CodeActionsProvider: quick-fix to add missing "use <module>" at top of file
// organizeImports(): command to add ALL missing imports at once
// ═══════════════════════════════════════════════════════════════════════════════

const SERV_STDLIB_MODULES = [
    { name: 'db',     detail: 'Database queries and transactions',      doc: '`db.query()`, `db.exec()`, `db.transaction()`' },
    { name: 'cache',  detail: 'Distributed caching via ServCache',      doc: '`cache.get()`, `cache.set()`, `cache.del()`, `cache.ttl()`' },
    { name: 'http',   detail: 'HTTP client requests',                   doc: '`http.get()`, `http.post()`, `http.put()`, `http.delete()`' },
    { name: 'queue',  detail: 'Message publishing via ServQueue',       doc: '`queue.publish()`, `queue.subscribe()`, `queue.ack()`' },
    { name: 'store',  detail: 'Object storage via ServStore',           doc: '`store.put()`, `store.get()`, `store.delete()`, `store.list()`' },
    { name: 'lock',   detail: 'Distributed locks via ServLock',         doc: '`lock.acquire()`, `lock.release()`, `lock.tryAcquire()`' },
    { name: 'cron',   detail: 'Scheduled jobs via ServCron',            doc: '`cron.register()`, `cron.list()`' },
    { name: 'mail',   detail: 'Email sending via ServMail',             doc: '`mail.send()`, `mail.template()`' },
    { name: 'flow',   detail: 'Workflow orchestration via ServFlow',    doc: '`flow.start()`, `flow.step()`, `flow.compensate()`' },
    { name: 'json',   detail: 'JSON encode/decode',                     doc: '`json.encode()`, `json.decode()`, `json.pretty()`' },
    { name: 'log',    detail: 'Structured logging',                     doc: '`log.info()`, `log.warn()`, `log.error()`, `log.debug()`' },
    { name: 'env',    detail: 'Environment variables',                  doc: '`env.get()`, `env.require()`' },
    { name: 'crypto', detail: 'Cryptographic utilities',                doc: '`crypto.hash()`, `crypto.sign()`, `crypto.verify()`' },
    { name: 'time',   detail: 'Time and duration utilities',            doc: '`time.now()`, `time.parse()`, `time.format()`' },
    { name: 'uuid',   detail: 'UUID generation',                        doc: '`uuid.v4()`, `uuid.v7()`' },
    { name: 'path',   detail: 'File path utilities',                    doc: '`path.join()`, `path.ext()`, `path.base()`' },
    { name: 'fs',     detail: 'File system access',                     doc: '`fs.read()`, `fs.write()`, `fs.exists()`, `fs.delete()`' },
    { name: 'math',   detail: 'Math utilities',                         doc: '`math.abs()`, `math.ceil()`, `math.floor()`, `math.min()`, `math.max()`' },
];

class ServImportCompletionProvider {
    provideCompletionItems(document, position, token, context) {
        const lineText = document.lineAt(position).text;
        const prefix   = lineText.substr(0, position.character);

        // Only trigger on lines that look like: "use <partial>"
        if (!/^\s*use\s+\w*$/.test(prefix)) return [];

        return SERV_STDLIB_MODULES.map(mod => {
            const item = new vscode.CompletionItem(mod.name, vscode.CompletionItemKind.Module);
            item.detail        = mod.detail;
            item.documentation = new vscode.MarkdownString(mod.doc);
            item.insertText    = mod.name;
            item.sortText      = '0' + mod.name;
            return item;
        });
    }
}

class ServImportCodeActionProvider {
    provideCodeActions(document, range, context, token) {
        const lines   = document.getText().split('\n');
        const used    = this._findUsedModules(lines);
        const present = this._findImportedModules(lines);
        const missing = used.filter(m => !present.has(m));

        if (missing.length === 0) return [];

        const actions = missing.map(mod => {
            const action = new vscode.CodeAction(
                `Add "use ${mod}"`,
                vscode.CodeActionKind.QuickFix
            );
            const edit = new vscode.WorkspaceEdit();
            edit.insert(document.uri, new vscode.Position(0, 0), `use ${mod}\n`);
            action.edit = edit;
            action.isPreferred = missing.length === 1;
            return action;
        });

        if (missing.length > 1) {
            const bulk = new vscode.CodeAction(
                `Add all missing imports (${missing.join(', ')})`,
                vscode.CodeActionKind.QuickFix
            );
            const edit = new vscode.WorkspaceEdit();
            edit.insert(document.uri, new vscode.Position(0, 0), missing.map(m => `use ${m}`).join('\n') + '\n');
            bulk.edit = edit;
            bulk.isPreferred = true;
            actions.push(bulk);
        }

        return actions;
    }

    _findUsedModules(lines) {
        const known = new Set(SERV_STDLIB_MODULES.map(m => m.name));
        const used  = new Set();
        for (const line of lines) {
            const t = line.trim();
            if (t.startsWith('use ') || t.startsWith('//') || t.startsWith('*')) continue;
            for (const mod of known) {
                if (new RegExp(`\\b${mod}\\.`).test(line)) used.add(mod);
            }
        }
        return [...used];
    }

    _findImportedModules(lines) {
        const present = new Set();
        for (const line of lines) {
            const m = /^\s*use\s+(\w+)/.exec(line);
            if (m) present.add(m[1]);
        }
        return present;
    }
}

async function organizeImports() {
    const editor = vscode.window.activeTextEditor;
    if (!editor || editor.document.languageId !== 'serv') {
        vscode.window.showWarningMessage('Open a .srv file to organize imports.');
        return;
    }
    const provider = new ServImportCodeActionProvider();
    const doc      = editor.document;
    const lines    = doc.getText().split('\n');
    const missing  = provider._findUsedModules(lines).filter(
        m => !provider._findImportedModules(lines).has(m)
    );

    if (missing.length === 0) {
        vscode.window.showInformationMessage('Serv: All imports are already present.');
        return;
    }

    const edit = new vscode.WorkspaceEdit();
    edit.insert(doc.uri, new vscode.Position(0, 0), missing.map(m => `use ${m}`).join('\n') + '\n');
    await vscode.workspace.applyEdit(edit);
    vscode.window.showInformationMessage(`Serv: Added ${missing.length} import(s): ${missing.join(', ')}`);
}
