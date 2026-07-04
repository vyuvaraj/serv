//go:build enterprise

package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

func (h *GatewayHandler) handleGraphQLFederation(w http.ResponseWriter, r *http.Request, route *Route) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		WriteJSONError(w, r, "Bad request: failed to read body", "ERR_BAD_REQUEST_BODY", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	var reqBody struct {
		Query         string                 `json:"query"`
		Variables     map[string]interface{} `json:"variables"`
		OperationName string                 `json:"operationName"`
	}
	if err := json.Unmarshal(bodyBytes, &reqBody); err != nil {
		WriteJSONError(w, r, "Bad request: invalid JSON payload", "ERR_INVALID_JSON", http.StatusBadRequest)
		return
	}

	// Basic AST field parser for simplicity (extract top-level fields inside query { ... })
	fields := extractGraphQLQueryFields(reqBody.Query)
	if len(fields) == 0 {
		WriteJSONError(w, r, "Bad request: no query fields detected", "ERR_BAD_QUERY", http.StatusBadRequest)
		return
	}

	type subResult struct {
		field string
		data  interface{}
		err   error
	}

	resultsChan := make(chan subResult, len(fields))
	var wg sync.WaitGroup

	for _, field := range fields {
		targetBackend, exists := route.GraphQLFederation[field]
		if !exists {
			// Fallback to default target
			targetBackend = route.Target
		}

		if targetBackend == "" {
			continue
		}

		wg.Add(1)
		go func(f, target string) {
			defer wg.Done()
			
			// Build field-specific single query: query { <field> { ... } }
			subQuery := fmt.Sprintf("query { %s }", rebuildQueryForField(reqBody.Query, f))
			subReqPayload := map[string]interface{}{
				"query": subQuery,
			}
			if reqBody.Variables != nil {
				subReqPayload["variables"] = reqBody.Variables
			}
			subBytes, _ := json.Marshal(subReqPayload)

			httpReq, err := http.NewRequestWithContext(r.Context(), "POST", target, bytes.NewReader(subBytes))
			if err != nil {
				resultsChan <- subResult{field: f, err: err}
				return
			}
			httpReq.Header.Set("Content-Type", "application/json")
			if auth := r.Header.Get("Authorization"); auth != "" {
				httpReq.Header.Set("Authorization", auth)
			}

			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Do(httpReq)
			if err != nil {
				resultsChan <- subResult{field: f, err: err}
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				rb, _ := io.ReadAll(resp.Body)
				resultsChan <- subResult{field: f, err: fmt.Errorf("backend status %d: %s", resp.StatusCode, string(rb))}
				return
			}

			var respData struct {
				Data   map[string]interface{} `json:"data"`
				Errors []interface{}          `json:"errors"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
				resultsChan <- subResult{field: f, err: err}
				return
			}

			if len(respData.Errors) > 0 {
				resultsChan <- subResult{field: f, err: fmt.Errorf("backend errors: %v", respData.Errors)}
				return
			}

			resultsChan <- subResult{field: f, data: respData.Data[f]}
		}(field, targetBackend)
	}

	wg.Wait()
	close(resultsChan)

	mergedData := make(map[string]interface{})
	var errors []string

	for res := range resultsChan {
		if res.err != nil {
			errors = append(errors, fmt.Sprintf("field %q: %v", res.field, res.err))
		} else {
			mergedData[res.field] = res.data
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	respPayload := map[string]interface{}{
		"data": mergedData,
	}
	if len(errors) > 0 {
		respPayload["errors"] = errors
	}
	_ = json.NewEncoder(w).Encode(respPayload)
}

func extractGraphQLQueryFields(query string) []string {
	normalized := strings.ReplaceAll(query, "\n", " ")
	normalized = strings.ReplaceAll(normalized, "\t", " ")
	normalized = strings.ReplaceAll(normalized, ",", " ")
	
	idx := strings.Index(normalized, "{")
	if idx == -1 {
		return nil
	}
	normalized = normalized[idx+1:]
	
	var fields []string
	depth := 0
	currentField := ""
	
	for i := 0; i < len(normalized); i++ {
		char := normalized[i]
		if char == '{' {
			depth++
			if depth == 1 {
				f := strings.TrimSpace(currentField)
				if f != "" {
					parts := strings.Fields(f)
					if len(parts) > 0 {
						fields = append(fields, parts[len(parts)-1])
					}
				}
				currentField = ""
			}
		} else if char == '}' {
			depth--
			if depth < 0 {
				break
			}
		} else {
			if depth == 0 {
				currentField += string(char)
			}
		}
	}
	return fields
}

func rebuildQueryForField(originalQuery, field string) string {
	normalized := originalQuery
	idx := strings.Index(normalized, field)
	if idx == -1 {
		return ""
	}
	
	block := normalized[idx:]
	braceIdx := strings.Index(block, "{")
	if braceIdx == -1 {
		return field
	}
	
	depth := 0
	endIdx := -1
	for i := braceIdx; i < len(block); i++ {
		if block[i] == '{' {
			depth++
		} else if block[i] == '}' {
			depth--
			if depth == 0 {
				endIdx = i
				break
			}
		}
	}
	if endIdx == -1 {
		return block
	}
	return block[:endIdx+1]
}
