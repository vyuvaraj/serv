package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"serv/compiler"
)

func runDocs() {
	// Support both:
	//   serv doc <file.srv> [-o <out.html>]  (HTML output, default)
	//   serv docs generate <file.srv> [-o <out.json>] (JSON output)
	
	cmd := os.Args[1] // "doc" or "docs"
	
	if cmd == "docs" {
		if len(os.Args) < 3 {
			fmt.Println("Usage: serv docs generate <file.srv> [-o <output-file>]")
			os.Exit(1)
		}
		subCommand := os.Args[2]
		if subCommand != "generate" {
			fmt.Printf("Unknown docs subcommand: %s. Did you mean 'generate'?\n", subCommand)
			os.Exit(1)
		}

		docsCmd := flag.NewFlagSet("docs generate", flag.ExitOnError)
		outputFile := docsCmd.String("o", "openapi.json", "Output file path")

		var options []string
		var fileArg string
		for i := 3; i < len(os.Args); i++ {
			arg := os.Args[i]
			if arg == "-o" && i+1 < len(os.Args) {
				options = append(options, "-o", os.Args[i+1])
				i++
			} else if strings.HasPrefix(arg, "-") {
				options = append(options, arg)
			} else {
				fileArg = arg
			}
		}

		if fileArg == "" {
			fmt.Println("Usage: serv docs generate <file.srv> [-o <output-file>]")
			os.Exit(1)
		}

		if err := docsCmd.Parse(options); err != nil {
			fmt.Printf("Error parsing options: %v\n", err)
			os.Exit(1)
		}

		_, prog, err := parseProject(fileArg)
		if err != nil {
			fmt.Printf("Error parsing project: %v\n", err)
			os.Exit(1)
		}

		jsonStr, err := compiler.GenerateOpenAPI(prog)
		if err != nil {
			fmt.Printf("Error generating OpenAPI: %v\n", err)
			os.Exit(1)
		}

		if err := os.WriteFile(*outputFile, []byte(jsonStr), 0644); err != nil {
			fmt.Printf("Error writing output file: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("✓ Successfully generated OpenAPI documentation at %s\n", *outputFile)
		return
	}

	// Else: serv doc <file.srv> [-o <out.html>]
	docCmd := flag.NewFlagSet("doc", flag.ExitOnError)
	outputFile := docCmd.String("o", "docs.html", "Output file path")

	var options []string
	var fileArg string
	for i := 2; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "-o" && i+1 < len(os.Args) {
			options = append(options, "-o", os.Args[i+1])
			i++
		} else if strings.HasPrefix(arg, "-") {
			options = append(options, arg)
		} else {
			fileArg = arg
		}
	}

	if fileArg == "" {
		fmt.Println("Usage: serv doc <file.srv> [-o <output-file.html>]")
		os.Exit(1)
	}

	if err := docCmd.Parse(options); err != nil {
		fmt.Printf("Error parsing options: %v\n", err)
		os.Exit(1)
	}

	_, prog, err := parseProject(fileArg)
	if err != nil {
		fmt.Printf("Error parsing project: %v\n", err)
		os.Exit(1)
	}

	// Extract the source code comments if any, but since the compiler does not preserve them in AST,
	// we will scan the srv source files and compile-doc them.
	// For now, let's call GenerateHTMLDocs
	htmlStr, err := compiler.GenerateHTMLDocs(prog, fileArg)
	if err != nil {
		fmt.Printf("Error generating HTML documentation: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(*outputFile, []byte(htmlStr), 0644); err != nil {
		fmt.Printf("Error writing output file: %v\n", err)
		os.Exit(1)
	}

	absPath, _ := filepath.Abs(*outputFile)
	fmt.Printf("✓ Successfully generated HTML documentation at %s\n", absPath)
}
