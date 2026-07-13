package main

import (
	"fmt"
	"os"
	"strings"
	"serv/compiler"
	"serv/runtime"
)

func runAIScaffold(prompt string, autoFix bool) {
	fmt.Printf("Generating project scaffolding for prompt: %q...\n", prompt)

	// Configure local LLM default fallback or environment key if present
	aiConnStr := os.Getenv("SERV_AI_CONNECTION")
	if aiConnStr == "" {
		aiConnStr = "openai://gpt-4o-mini"
	}
	runtime.InitAI(aiConnStr)

	systemPrompt := `You are an expert software scaffolding assistant for the Serv programming language.
Your task is to write a single ".srv" file that fulfills the user's requirements.
Follow these rules strictly:
1. ONLY return the code blocks of Serv programming language.
2. DO NOT wrap with Markdown code fences. Return raw Serv code.
3. Incorporate required route, model, server, database, every, or cron statements as described.

Serv DSL Syntax Quick Reference:
- server "8080"
- database "sqlite://dev.db"
- model User { id: integer, name: string }
- route "GET" "/users" (req) { return db.query("SELECT * FROM users;"); }
- every 10s { log.info("Running job"); }
- cron "0 0 * * *" { log.info("Daily job"); }
`

	currentPrompt := prompt
	maxAttempts := 1
	if autoFix {
		maxAttempts = 4
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		payload := map[string]interface{}{
			"prompt":      fmt.Sprintf("System Instructions:\n%s\n\nUser Request: %s", systemPrompt, currentPrompt),
			"max_tokens":  2048,
			"temperature": 0.2,
		}

		var result interface{}
		if mockResp := os.Getenv("SERV_TEST_AI_RESPONSE"); mockResp != "" {
			result = mockResp
		} else {
			result = runtime.AIComplete(payload)
		}

		if result == nil {
			fmt.Println("AI scaffolding generator failed: AI provider did not return a response.")
			os.Exit(1)
		}

		code := fmt.Sprint(result)
		// Clean up markdown block fences if returned by LLM
		code = strings.TrimPrefix(code, "```serv")
		code = strings.TrimPrefix(code, "```")
		code = strings.TrimSuffix(code, "```")
		code = strings.TrimSpace(code)

		// Validate the generated code
		lexer := compiler.NewLexer(code)
		parser := compiler.NewParser(lexer)
		program := parser.ParseProgram()

		var errors []string
		for _, errMsg := range parser.Errors() {
			errors = append(errors, errMsg)
		}

		diags := compiler.Analyze(program)
		for _, d := range diags {
			if d.Severity == "error" {
				errors = append(errors, fmt.Sprintf("Line %d Col %d: %s", d.Line, d.Col, d.Message))
			}
		}

		if len(errors) == 0 {
			outputFile := "main.srv"
			if err := os.WriteFile(outputFile, []byte(code), 0644); err != nil {
				fmt.Printf("Failed to write scaffolded code to %s: %v\n", outputFile, err)
				os.Exit(1)
			}
			fmt.Printf("Successfully scaffolded service code into %s!\n", outputFile)
			return
		}

		if attempt == maxAttempts {
			fmt.Printf("AI scaffolding generator failed after %d attempts with errors:\n%s\n", attempt, strings.Join(errors, "\n"))
			os.Exit(1)
		}

		fmt.Printf("Attempt %d generated code has compiler/analysis errors. Retrying with feedback...\n", attempt)
		currentPrompt = fmt.Sprintf("%s\n\nYour previous code had compilation errors:\n%s\n\nPlease fix the errors and provide the corrected Serv code.", prompt, strings.Join(errors, "\n"))
	}
}
