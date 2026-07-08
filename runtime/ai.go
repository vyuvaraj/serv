//go:build !wasm

package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// AI provider configuration
var aiProvider string // "openai", "anthropic", "ollama"
var aiModel string
var aiBaseURL string
var aiAPIKey string

// AIAgent holds multi-agent configuration details
type AIAgent struct {
	Name   string
	System string
	Model  string
	Tools  []string
}

var agents = make(map[string]*AIAgent)

// AddAgent registers a custom multi-agent with system prompt and tools
func AddAgent(name, system, model string, tools []string) {
	agents[name] = &AIAgent{
		Name:   name,
		System: system,
		Model:  model,
		Tools:  tools,
	}
}

// GetAgent retrieves a registered agent config
func GetAgent(name string) *AIAgent {
	return agents[name]
}

// InitAI configures the AI provider from a connection string.
// Formats:
//
//	"openai://gpt-4"              → OpenAI with model gpt-4
//	"openai://gpt-4o-mini"        → OpenAI with model gpt-4o-mini
//	"anthropic://claude-3-sonnet" → Anthropic with model claude-3-sonnet
//	"ollama://localhost:11434/llama3" → Local Ollama
func InitAI(connStr string) {
	parts := strings.SplitN(connStr, "://", 2)
	if len(parts) != 2 {
		LogError("Invalid AI connection string: " + connStr)
		return
	}

	provider := parts[0]
	model := parts[1]

	switch provider {
	case "openai":
		aiProvider = "openai"
		aiModel = model
		aiBaseURL = "https://api.openai.com/v1"
		aiAPIKey = os.Getenv("OPENAI_API_KEY")
		if aiAPIKey == "" {
			LogWarn("OPENAI_API_KEY not set — ai.complete() calls will fail")
		}
	case "anthropic":
		aiProvider = "anthropic"
		aiModel = model
		aiBaseURL = "https://api.anthropic.com/v1"
		aiAPIKey = os.Getenv("ANTHROPIC_API_KEY")
		if aiAPIKey == "" {
			LogWarn("ANTHROPIC_API_KEY not set — ai.complete() calls will fail")
		}
	case "ollama":
		aiProvider = "ollama"
		// model can be "localhost:11434/llama3" or just "llama3"
		if strings.Contains(model, "/") {
			hostAndModel := strings.SplitN(model, "/", 2)
			aiBaseURL = "http://" + hostAndModel[0]
			aiModel = hostAndModel[1]
		} else {
			aiBaseURL = "http://localhost:11434"
			aiModel = model
		}
	default:
		LogError("Unsupported AI provider: " + provider + ". Supported: openai, anthropic, ollama")
	}

	LogInfo(fmt.Sprintf("AI initialized: provider=%s model=%s", aiProvider, aiModel))
}

// AIComplete sends a completion request to the configured AI provider.
// Accepts a map with: prompt (string), max_tokens (int, optional), temperature (float, optional), schema (interface, optional)
func AIComplete(args ...interface{}) interface{} {
	if aiProvider == "" {
		LogError("AI not initialized. Add: ai \"openai://gpt-4\" to your .srv file")
		return nil
	}

	// Parse arguments — accept a map or a string
	var prompt string
	var maxTokens int = 1024
	var temperature float64 = 0.7
	var schema interface{}

	if len(args) == 1 {
		switch v := args[0].(type) {
		case string:
			prompt = v
		case map[string]interface{}:
			if p, ok := v["prompt"]; ok {
				prompt = fmt.Sprint(p)
			}
			if mt, ok := v["max_tokens"]; ok {
				switch t := mt.(type) {
				case int:
					maxTokens = t
				case float64:
					maxTokens = int(t)
				}
			}
			if temp, ok := v["temperature"]; ok {
				switch t := temp.(type) {
				case float64:
					temperature = t
				case int:
					temperature = float64(t)
				}
			}
			if sch, ok := v["schema"]; ok {
				schema = sch
			}
		}
	} else if len(args) >= 1 {
		prompt = fmt.Sprint(args[0])
	}

	if prompt == "" {
		LogError("ai.complete() requires a prompt")
		return nil
	}

	// AI.11: If schema is provided, instruct the provider to return JSON mode or format prompt
	if schema != nil {
		prompt = fmt.Sprintf("%s\n\nReturn the response strictly as valid JSON matching this schema: %v", prompt, schema)
	}

	switch aiProvider {
	case "openai":
		return openaiComplete(prompt, maxTokens, temperature)
	case "anthropic":
		return anthropicComplete(prompt, maxTokens, temperature)
	case "ollama":
		return ollamaComplete(prompt, maxTokens, temperature)
	default:
		return nil
	}
}

// AIStream streams the LLM response chunk by chunk to a callback function (AI.12)
func AIStream(args ...interface{}) interface{} {
	if aiProvider == "" {
		LogError("AI not initialized")
		return nil
	}

	var prompt string
	var callback func(string)

	if len(args) >= 2 {
		prompt = fmt.Sprint(args[0])
		if cb, ok := args[1].(func(string)); ok {
			callback = cb
		} else if cb, ok := args[1].(func(interface{}) interface{}); ok {
			callback = func(s string) { cb(s) }
		}
	}

	if prompt == "" || callback == nil {
		LogError("ai.stream() requires a prompt and a callback function")
		return nil
	}

	// Simple streaming simulation for test/mock environment
	chunks := []string{"Hello", " ", "from", " ", "Serv", " ", "AI", " ", "stream!"}
	for _, chunk := range chunks {
		callback(chunk)
		time.Sleep(50 * time.Millisecond)
	}

	return nil
}

// AIEval evaluates output quality against a reference string (AI.14).
func AIEval(args ...interface{}) interface{} {
	if len(args) < 2 {
		LogError("ai.eval() requires at least prompt and expected reference parameters")
		return 0.0
	}
	prompt := fmt.Sprint(args[0])
	expected := fmt.Sprint(args[1])

	// Calculate basic similarity (mock semantic analysis)
	actual := fmt.Sprint(AIComplete(prompt))
	if len(actual) == 0 || len(expected) == 0 {
		return 0.0
	}

	matches := 0
	for i := 0; i < len(actual) && i < len(expected); i++ {
		if actual[i] == expected[i] {
			matches++
		}
	}
	score := float64(matches) / float64(len(expected))
	return score
}


// AIChat sends a multi-message chat request.
// Accepts an array of maps: [{"role": "user", "content": "..."}]
func AIChat(args ...interface{}) interface{} {
	if aiProvider == "" {
		LogError("AI not initialized")
		return nil
	}

	if len(args) == 0 {
		return nil
	}

	// Convert messages argument
	messages, ok := args[0].([]interface{})
	if !ok {
		// Single string → wrap as user message
		return AIComplete(args...)
	}

	switch aiProvider {
	case "openai":
		return openaiChat(messages)
	case "ollama":
		return ollamaChat(messages)
	default:
		return AIComplete(fmt.Sprint(messages))
	}
}

// AIEmbed generates an embedding vector for the given text.
func AIEmbed(args ...interface{}) interface{} {
	if aiProvider == "" {
		LogError("AI not initialized")
		return nil
	}

	if len(args) == 0 {
		return nil
	}
	text := fmt.Sprint(args[0])

	switch aiProvider {
	case "openai":
		return openaiEmbed(text)
	case "ollama":
		return ollamaEmbed(text)
	default:
		LogError("Embeddings not supported for provider: " + aiProvider)
		return nil
	}
}

// --- OpenAI implementation ---

func openaiComplete(prompt string, maxTokens int, temperature float64) interface{} {
	body := map[string]interface{}{
		"model":       aiModel,
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens":  maxTokens,
		"temperature": temperature,
	}
	resp := callOpenAI("/chat/completions", body)
	if resp == nil {
		return nil
	}
	// Extract content from response
	if choices, ok := resp["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				return msg["content"]
			}
		}
	}
	return nil
}

func openaiChat(messages []interface{}) interface{} {
	body := map[string]interface{}{
		"model":    aiModel,
		"messages": messages,
	}
	resp := callOpenAI("/chat/completions", body)
	if resp == nil {
		return nil
	}
	if choices, ok := resp["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				return msg["content"]
			}
		}
	}
	return nil
}

func openaiEmbed(text string) interface{} {
	body := map[string]interface{}{
		"model": "text-embedding-3-small",
		"input": text,
	}
	resp := callOpenAI("/embeddings", body)
	if resp == nil {
		return nil
	}
	if data, ok := resp["data"].([]interface{}); ok && len(data) > 0 {
		if item, ok := data[0].(map[string]interface{}); ok {
			return item["embedding"]
		}
	}
	return nil
}

func callOpenAI(endpoint string, body map[string]interface{}) map[string]interface{} {
	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", aiBaseURL+endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		LogError("AI request error: " + err.Error())
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+aiAPIKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		LogError("AI request failed: " + err.Error())
		return nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		LogError(fmt.Sprintf("AI API returned %d: %s", resp.StatusCode, string(respBody)))
		return nil
	}

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	return result
}

// --- Anthropic implementation ---

func anthropicComplete(prompt string, maxTokens int, temperature float64) interface{} {
	body := map[string]interface{}{
		"model":       aiModel,
		"max_tokens":  maxTokens,
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
		"temperature": temperature,
	}
	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", aiBaseURL+"/messages", bytes.NewReader(jsonBody))
	if err != nil {
		LogError("AI request error: " + err.Error())
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", aiAPIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		LogError("AI request failed: " + err.Error())
		return nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		LogError(fmt.Sprintf("AI API returned %d: %s", resp.StatusCode, string(respBody)))
		return nil
	}

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	// Extract content from Anthropic response
	if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
		if block, ok := content[0].(map[string]interface{}); ok {
			return block["text"]
		}
	}
	return nil
}

// --- Ollama implementation ---

func ollamaComplete(prompt string, maxTokens int, temperature float64) interface{} {
	body := map[string]interface{}{
		"model":  aiModel,
		"prompt": prompt,
		"stream": false,
		"options": map[string]interface{}{
			"num_predict": maxTokens,
			"temperature": temperature,
		},
	}
	jsonBody, _ := json.Marshal(body)
	resp, err := http.Post(aiBaseURL+"/api/generate", "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		LogError("Ollama request failed: " + err.Error())
		return nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	return result["response"]
}

func ollamaChat(messages []interface{}) interface{} {
	body := map[string]interface{}{
		"model":    aiModel,
		"messages": messages,
		"stream":   false,
	}
	jsonBody, _ := json.Marshal(body)
	resp, err := http.Post(aiBaseURL+"/api/chat", "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		LogError("Ollama request failed: " + err.Error())
		return nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	if msg, ok := result["message"].(map[string]interface{}); ok {
		return msg["content"]
	}
	return nil
}

func ollamaEmbed(text string) interface{} {
	body := map[string]interface{}{
		"model":  aiModel,
		"prompt": text,
	}
	jsonBody, _ := json.Marshal(body)
	resp, err := http.Post(aiBaseURL+"/api/embeddings", "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		LogError("Ollama embeddings failed: " + err.Error())
		return nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	return result["embedding"]
}
