package compiler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// analyzeTopicSchemas performs static linting on publish and subscribe topics.
func analyzeTopicSchemas(program *Program) []Diagnostic {
	var diags []Diagnostic

	// 1. Gather all struct declarations
	structs := make(map[string]*StructDecl)
	for _, stmt := range program.Statements {
		switch s := stmt.(type) {
		case *StructDecl:
			structs[s.Name] = s
		case *ExportStmt:
			if sInner, ok := s.Inner.(*StructDecl); ok {
				structs[sInner.Name] = sInner
			}
		}
	}

	// Helper to fetch schema from registry or local file
	getSchemaForTopic := func(topicName string) (map[string]interface{}, error) {
		// First check local schemas/ directory
		localPath := filepath.Join("schemas", topicName+".json")
		if _, err := os.Stat(localPath); err == nil {
			data, err := os.ReadFile(localPath)
			if err == nil {
				var schema map[string]interface{}
				if err := json.Unmarshal(data, &schema); err == nil {
					return schema, nil
				}
			}
		}

		// Fallback/direct check against running ServRegistry
		// Default to localhost:8080 or use SERV_REGISTRY_URL
		registryURL := os.Getenv("SERV_REGISTRY_URL")
		if registryURL == "" {
			registryURL = "http://localhost:8080"
		}
		url := fmt.Sprintf("%s/api/v1/schemas/%s", registryURL, topicName)
		resp, err := http.Get(url)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			data, err := io.ReadAll(resp.Body)
			if err == nil {
				var schema map[string]interface{}
				if err := json.Unmarshal(data, &schema); err == nil {
					return schema, nil
				}
			}
		}
		return nil, fmt.Errorf("schema not found")
	}

	// Walk all statements to find PublishStmt and SubscribeStmt
	var checkStmt func(stmt Statement)
	checkStmt = func(stmt Statement) {
		if isNil(stmt) {
			return
		}

		switch s := stmt.(type) {
		case *PublishStmt:
			// Check if topic is a string literal
			if strLit, ok := s.Topic.(*StringLiteral); ok {
				topicName := strLit.Value
				schema, err := getSchemaForTopic(topicName)
				if err == nil {
					// We found a schema! Let's validate the published value
					// If value is a StructLiteral:
					if valStruct, ok := s.Value.(*StructLiteral); ok {
						diags = append(diags, validateStructLiteralAgainstSchema(valStruct, schema)...)
					}
				}
			}
		case *SubscribeStmt:
			// Check if topic is a string literal
			if strLit, ok := s.Topic.(*StringLiteral); ok {
				topicName := strLit.Value
				_, err := getSchemaForTopic(topicName)
				if err == nil {
					// We can check if the subscriber body uses fields that don't exist in the schema
					// (e.g. if the parameter is destured or accessed, but since it's dynamic, let's focus on:
					// if there is a struct definition matching the topic type or if we can do basic validation).
				}
			}
		case *BlockStmt:
			for _, child := range s.Statements {
				checkStmt(child)
			}
		case *FnDecl:
			checkStmt(s.Body)
		case *RouteStmt:
			checkStmt(s.Body)
		case *EveryStmt:
			checkStmt(s.Body)
		case *CronStmt:
			checkStmt(s.Body)
		case *IfStmt:
			checkStmt(s.Body)
			if s.ElseBody != nil {
				checkStmt(s.ElseBody)
			}
		case *ForStmt:
			checkStmt(s.Body)
		case *TryCatchStmt:
			checkStmt(s.TryBody)
			checkStmt(s.CatchBody)
		case *MatchStmt:
			for _, c := range s.Cases {
				checkStmt(c.Body)
			}
		case *ExportStmt:
			checkStmt(s.Inner)
		}
	}

	for _, stmt := range program.Statements {
		checkStmt(stmt)
	}

	return diags
}

func validateStructLiteralAgainstSchema(structLit *StructLiteral, schema map[string]interface{}) []Diagnostic {
	var diags []Diagnostic
	
	// Get properties from schema
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return nil
	}

	// 1. Check required fields
	if reqList, ok := schema["required"].([]interface{}); ok {
		for _, rKeyVal := range reqList {
			if rKey, ok := rKeyVal.(string); ok {
				if _, ok := structLit.Fields[rKey]; !ok {
					diags = append(diags, Diagnostic{
						Line:     structLit.Token.Line,
						Col:      structLit.Token.Col,
						Severity: "error",
						Message:  fmt.Sprintf("Schema mismatch: published value is missing required property '%s' for topic schema", rKey),
					})
				}
			}
		}
	}

	// 2. Check field types
	for key, valExpr := range structLit.Fields {
		propVal, ok := props[key]
		if !ok {
			// Field not in schema - warn that this field is not defined in the schema
			diags = append(diags, Diagnostic{
				Line:     structLit.Token.Line,
				Col:      structLit.Token.Col,
				Severity: "warning",
				Message:  fmt.Sprintf("Schema warning: property '%s' is not defined in the topic schema", key),
			})
			continue
		}

		propMap, ok := propVal.(map[string]interface{})
		if !ok {
			continue
		}

		expectedType, _ := propMap["type"].(string)
		if expectedType == "" {
			continue
		}

		// Try to deduce the type of valExpr
		actualType := getExpressionType(valExpr)
		if actualType != "" && !typesAreCompatible(actualType, expectedType) {
			diags = append(diags, Diagnostic{
				Line:     structLit.Token.Line,
				Col:      structLit.Token.Col,
				Severity: "error",
				Message:  fmt.Sprintf("Schema mismatch: property '%s' expects type '%s', but got '%s'", key, expectedType, actualType),
			})
		}
	}

	return diags
}

// Simple type deduction for expressions during linting
func getExpressionType(expr Expression) string {
	switch e := expr.(type) {
	case *StringLiteral, *FStringLiteral:
		return "string"
	case *IntegerLiteral:
		return "integer"
	case *FloatLiteral:
		return "number"
	case *BooleanLiteral:
		return "boolean"
	case *StructLiteral:
		return e.TypeName
	}
	return ""
}

func typesAreCompatible(actual, expected string) bool {
	actual = strings.ToLower(actual)
	expected = strings.ToLower(expected)
	if actual == expected {
		return true
	}
	if (actual == "integer" || actual == "number" || actual == "int" || actual == "float") &&
		(expected == "integer" || expected == "number" || expected == "int" || expected == "float") {
		return true
	}
	if actual == "boolean" && expected == "bool" {
		return true
	}
	if actual == "bool" && expected == "boolean" {
		return true
	}
	return false
}
