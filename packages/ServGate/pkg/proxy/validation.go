package proxy

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ValidateRequest checks the body of an HTTP request against a Route's validation schema.
func ValidateRequest(bodyBytes []byte, schema map[string]string) error {
	if len(schema) == 0 {
		return nil
	}

	var data map[string]interface{}
	if len(bodyBytes) == 0 {
		data = make(map[string]interface{})
	} else {
		if err := json.Unmarshal(bodyBytes, &data); err != nil {
			return fmt.Errorf("Validation failed: request body is not valid JSON")
		}
	}

	for field, rule := range schema {
		parts := strings.Split(rule, ",")
		required := false
		var expectedType string

		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "required" {
				required = true
			} else if p != "" {
				expectedType = p
			}
		}

		val, exists := data[field]
		if !exists || val == nil {
			if required {
				return fmt.Errorf("Validation failed: field '%s' is required", field)
			}
			continue
		}

		// Validate types
		switch expectedType {
		case "string":
			if _, ok := val.(string); !ok {
				return fmt.Errorf("Validation failed: field '%s' must be a string", field)
			}
		case "email":
			strVal, ok := val.(string)
			if !ok {
				return fmt.Errorf("Validation failed: field '%s' must be a string", field)
			}
			if !strings.Contains(strVal, "@") || !strings.Contains(strVal, ".") {
				return fmt.Errorf("Validation failed: field '%s' must be a valid email", field)
			}
		case "int", "integer":
			numVal, ok := val.(float64)
			if !ok {
				return fmt.Errorf("Validation failed: field '%s' must be an integer", field)
			}
			if numVal != float64(int(numVal)) {
				return fmt.Errorf("Validation failed: field '%s' must be an integer", field)
			}
		case "float", "number", "double":
			if _, ok := val.(float64); !ok {
				return fmt.Errorf("Validation failed: field '%s' must be a number", field)
			}
		case "bool", "boolean":
			if _, ok := val.(bool); !ok {
				return fmt.Errorf("Validation failed: field '%s' must be a boolean", field)
			}
		}
	}

	return nil
}
