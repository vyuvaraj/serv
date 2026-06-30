package main

import (
	"flag"
	"fmt"
	"os"
)

func runLspActionCmd(args []string) {
	fs := flag.NewFlagSet("lsp-action", flag.ExitOnError)
	file := fs.String("file", "", "Path to the .srv file")
	line := fs.Int("line", 0, "Line number for code action lookup")
	actionType := fs.String("type", "quickfix", "Type of action (quickfix, refactor)")

	_ = fs.Parse(args)

	if *file == "" || *line <= 0 {
		fmt.Println("Error: --file and --line parameters are required.")
		os.Exit(1)
	}

	// Read first line or check if exists
	if _, err := os.Stat(*file); err != nil {
		fmt.Printf("Error: file %s not found\n", *file)
		os.Exit(1)
	}

	fmt.Printf(`{"file": %q, "line": %d, "type": %q, "actions": [`, *file, *line, *actionType)
	fmt.Printf(`{"title": "Extract function", "kind": "refactor.extract", "edit": "Extracted helper function"},`)
	fmt.Printf(`{"title": "Add error handling", "kind": "quickfix.add_error", "edit": "Add try/catch statement"}`)
	fmt.Println(`]}`)
}
