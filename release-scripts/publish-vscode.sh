#!/bin/bash
# Package and publish the VS Code extension to the marketplace.
#
# Prerequisites:
#   npm install -g @vscode/vsce
#   vsce login serv-lang  (one-time setup with Personal Access Token)
#
# Usage: ./release-scripts/publish-vscode.sh

set -e

cd vscode-support/extension

echo "Packaging VS Code extension..."
vsce package

echo ""
echo "Publishing to VS Code Marketplace..."
vsce publish

echo ""
echo "Done! Extension published."
echo "Users can now install via: ext install serv-lang.serv-vscode"
