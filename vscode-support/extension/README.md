# Serv VS Code Extension

This is a local Visual Studio Code extension that provides syntax highlighting and bracket/comment configurations for the **Serv** programming language (`.srv` files).

## Installation

To install this extension locally in your VS Code editor:

### Option 1: Copy to Extensions Folder (Recommended)
Copy the `serv-vscode` directory directly into your VS Code extensions folder:

- **Windows**: Copy to `%USERPROFILE%\.vscode\extensions\serv-vscode`
  - In PowerShell:
    ```powershell
    Copy-Item -Recurse -Path "f:\Don\New folder\language\serv-vscode" -Destination "$env:USERPROFILE\.vscode\extensions\serv-vscode"
    ```
- **macOS / Linux**: Copy to `~/.vscode/extensions/serv-vscode`
  - In Terminal:
    ```bash
    cp -r "path/to/serv-vscode" ~/.vscode/extensions/serv-vscode
    ```

Once copied, restart or reload VS Code (`Ctrl+R` or command palette `Developer: Reload Window`), and all `.srv` files will automatically have syntax highlighting and brackets configuration.

### Option 2: Symbolic Link
Instead of copying, you can create a symbolic link so that any changes to this folder immediately apply:

- **Windows (Admin Command Prompt)**:
  ```cmd
  mklink /D "%USERPROFILE%\.vscode\extensions\serv-vscode" "f:\Don\New folder\language\serv-vscode"
  ```
- **macOS / Linux (Terminal)**:
  ```bash
  ln -s "$(pwd)/serv-vscode" ~/.vscode/extensions/serv-vscode
  ```
