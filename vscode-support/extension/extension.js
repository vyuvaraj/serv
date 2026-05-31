const vscode = require('vscode');
const cp = require('child_process');
const path = require('path');
const fs = require('fs');

/**
 * @param {vscode.ExtensionContext} context
 */
function activate(context) {
    const diagnosticCollection = vscode.languages.createDiagnosticCollection('serv');
    context.subscriptions.push(diagnosticCollection);

    // 1. Diagnostics / Linter
    const triggerLint = (document) => {
        if (document.languageId !== 'serv') return;

        let workspaceFolders = vscode.workspace.workspaceFolders;
        let servPath = 'serv'; // default in PATH

        if (workspaceFolders && workspaceFolders.length > 0) {
            let root = workspaceFolders[0].uri.fsPath;
            let winPath = path.join(root, 'serv.exe');
            let nixPath = path.join(root, 'serv');
            if (fs.existsSync(winPath)) {
                servPath = winPath;
            } else if (fs.existsSync(nixPath)) {
                servPath = nixPath;
            }
        }

        cp.execFile(servPath, ['lint', document.fileName], (err, stdout, stderr) => {
            let diagnostics = [];
            // Parse stdout or stderr for compiler errors
            let output = stdout + "\n" + stderr;
            let lines = output.split('\n');
            let regex = /\[Line (\d+), Col (\d+)\] (.*)/;

            for (let line of lines) {
                let match = regex.exec(line);
                if (match) {
                    let lineNum = parseInt(match[1], 10) - 1;
                    let colNum = parseInt(match[2], 10) - 1;
                    let msg = match[3].trim();
                    let range = new vscode.Range(lineNum, colNum, lineNum, colNum + 5);
                    let diagnostic = new vscode.Diagnostic(range, msg, vscode.DiagnosticSeverity.Error);
                    diagnostics.push(diagnostic);
                }
            }
            diagnosticCollection.set(document.uri, diagnostics);
        });
    };

    // Lint on open, save, and change
    vscode.workspace.textDocuments.forEach(triggerLint);
    context.subscriptions.push(vscode.workspace.onDidOpenTextDocument(triggerLint));
    context.subscriptions.push(vscode.workspace.onDidSaveTextDocument(triggerLint));
    context.subscriptions.push(vscode.workspace.onDidCloseTextDocument(doc => diagnosticCollection.delete(doc.uri)));

    // 2. Formatting Provider
    const formattingProvider = vscode.languages.registerDocumentFormattingEditProvider('serv', {
        provideDocumentFormattingEdits(document) {
            let edits = [];
            let indentLevel = 0;
            let tabSize = vscode.workspace.getConfiguration('editor').get('tabSize') || 4;
            let indentStr = " ".repeat(tabSize);
            let lineCount = document.lineCount;

            for (let i = 0; i < lineCount; i++) {
                let line = document.lineAt(i);
                let text = line.text.trim();

                if (text === "") {
                    if (line.text !== "") {
                        edits.push(vscode.TextEdit.replace(line.range, ""));
                    }
                    continue;
                }

                // Check closing braces at start of line
                let startsWithClosing = text.startsWith("}") || text.startsWith("]");
                if (startsWithClosing) {
                    indentLevel = Math.max(0, indentLevel - 1);
                }

                let formattedLine = indentStr.repeat(indentLevel) + text;

                // Calculate next line indent level
                let openBraces = (text.match(/\{|\[/g) || []).length;
                let closeBraces = (text.match(/\}|\]/g) || []).length;
                indentLevel += (openBraces - closeBraces);
                if (indentLevel < 0) indentLevel = 0;

                if (line.text !== formattedLine) {
                    edits.push(vscode.TextEdit.replace(line.range, formattedLine));
                }
            }
            return edits;
        }
    });
    context.subscriptions.push(formattingProvider);

    // 3. Hover Provider
    const builtinsHover = {
        "route": "Defines an HTTP route endpoint.\n\nSyntax:\n`route \"METHOD\" \"/path\" (req) { ... }`",
        "every": "Runs a background task periodically at the specified interval.\n\nSyntax:\n`every 5s { ... }`",
        "cron": "Runs a background task scheduled by a cron expression.\n\nSyntax:\n`cron \"*/10 * * * * *\" { ... }` or `cron env(\"VAR\") { ... }`",
        "subscribe": "Subscribes to messages on a broker topic channel.\n\nSyntax:\n`subscribe \"topic\" (msg) { ... }`",
        "publish": "Publishes a message to a broker topic channel.\n\nSyntax:\n`publish \"topic\" payload`",
        "spawn": "Spawns a lightweight concurrent thread/worker.\n\nSyntax:\n`spawn functionCall()` or `spawn(maxWorkers) functionCall()`",
        "db.query": "Executes a query on the configured database.\n\nSyntax:\n`db.query(\"SQL_QUERY\", ...arguments)` or `db.query(\"MongoDBCommand\", ...args)`",
        "cache.set": "Caches a key-value pair for a specific duration.\n\nSyntax:\n`cache.set(\"key\", value, \"5m\")`",
        "cache.get": "Retrieves a value from the cache.\n\nSyntax:\n`cache.get(\"key\")`",
        "json.parse": "Parses a JSON string into an object/map.\n\nSyntax:\n`json.parse(jsonString)`",
        "json.stringify": "Serializes an object/map into a JSON string.\n\nSyntax:\n`json.stringify(obj)`",
        "log.info": "Logs an informational message.\n\nSyntax:\n`log.info(...args)`",
        "log.warn": "Logs a warning message.\n\nSyntax:\n`log.warn(...args)`",
        "log.error": "Logs an error message.\n\nSyntax:\n`log.error(...args)`",
        "assert": "Asserts that an expression evaluates to truthy. Fails the test block otherwise.\n\nSyntax:\n`assert expression`",
        "test": "Declares a test block run by the Serv test runner.\n\nSyntax:\n`test \"description\" { ... }`"
    };

    const hoverProvider = vscode.languages.registerHoverProvider('serv', {
        provideHover(document, position) {
            let range = document.getWordRangeAtPosition(position);
            if (!range) return null;

            let word = document.getText(range);
            // Check for member actions like db.query or cache.set
            let line = document.lineAt(position.line).text;
            let fullWord = word;

            // Simple prefix check (e.g. log.info, db.query, cache.set)
            let prevDotIndex = line.lastIndexOf('.', range.start.character - 1);
            if (prevDotIndex !== -1 && prevDotIndex >= range.start.character - 6) {
                let prefixRange = document.getWordRangeAtPosition(new vscode.Position(position.line, prevDotIndex - 1));
                if (prefixRange) {
                    fullWord = document.getText(prefixRange) + "." + word;
                }
            }

            if (builtinsHover[fullWord]) {
                return new vscode.Hover(new vscode.MarkdownString(builtinsHover[fullWord]));
            }
            if (builtinsHover[word]) {
                return new vscode.Hover(new vscode.MarkdownString(builtinsHover[word]));
            }
            return null;
        }
    });
    context.subscriptions.push(hoverProvider);

    // 4. CodeLens Provider
    const codeLensProvider = vscode.languages.registerCodeLensProvider('serv', {
        provideCodeLenses(document) {
            let lenses = [];
            let lineCount = document.lineCount;

            for (let i = 0; i < lineCount; i++) {
                let line = document.lineAt(i).text.trim();
                if (line.startsWith('test ') || line.startsWith('test"')) {
                    let range = new vscode.Range(i, 0, i, line.length);
                    lenses.push(new vscode.CodeLens(range, {
                        title: "▶ Run Test Block",
                        command: "workbench.action.tasks.runTask",
                        arguments: ["Serv: Test Current File"]
                    }));
                }
                if (line.startsWith('route ') || line.startsWith('route"')) {
                    let range = new vscode.Range(i, 0, i, line.length);
                    lenses.push(new vscode.CodeLens(range, {
                        title: "⚡ Start Web Service",
                        command: "workbench.action.tasks.runTask",
                        arguments: ["Serv: Run Current File"]
                    }));
                }
            }
            return lenses;
        }
    });
    context.subscriptions.push(codeLensProvider);
}

function deactivate() {}

module.exports = {
    activate,
    deactivate
};
