package runtime

import (
	"fmt"
	"strings"
)

// PromptTemplate represents a prompt with template variable replacement.
type PromptTemplate struct {
	Template string
	Version  string
}

var promptRegistry = map[string]PromptTemplate{
	"summarize": {
		Template: "Summarize the following text in less than 3 sentences:\n\n{text}",
		Version:  "1.0.0",
	},
	"translate": {
		Template: "Translate the following text to {language}:\n\n{text}",
		Version:  "1.0.0",
	},
}

// RenderPrompt retrieves a registered prompt template and replaces variables (AI.13).
func RenderPrompt(name string, vars map[string]interface{}) (string, error) {
	tmpl, exists := promptRegistry[name]
	if !exists {
		return "", fmt.Errorf("prompt template %q not found", name)
	}

	result := tmpl.Template
	for k, v := range vars {
		result = strings.ReplaceAll(result, "{"+k+"}", fmt.Sprint(v))
	}
	return result, nil
}

// RegisterPromptTemplate registers a custom template.
func RegisterPromptTemplate(name string, template string, version string) {
	promptRegistry[name] = PromptTemplate{
		Template: template,
		Version:  version,
	}
}
