package main

import (
	"os"
	"strings"
	"testing"
	
	"serv/compiler"
)

func TestClientSDKGeneration(t *testing.T) {
	srvContent := `
struct User {
    id: int,
    username: string,
    active: bool
}

export struct Task {
    id: int,
    title: string,
    done: bool
}

route "GET" "/users" (req) {
    return { "users": [] }
}

route "GET" "/users/:id" (req) {
    return { "user": {} }
}

route "POST" "/tasks" (req) {
    return { "success": true }
}
`
	tmpFile := "temp_test_generate.srv"
	if err := os.WriteFile(tmpFile, []byte(srvContent), 0644); err != nil {
		t.Fatalf("Failed to write temporary test file: %v", err)
	}
	defer os.Remove(tmpFile)

	_, prog, err := parseProject(tmpFile)
	if err != nil {
		t.Fatalf("Failed to parse temporary project: %v", err)
	}

	// Create generator
	g := NewClientGeneratorFromProgram(prog) // We can call a helper wrapper or instantiate directly

	// Test TypeScript
	tsCode, err := g.GenerateTypeScript()
	if err != nil {
		t.Fatalf("TypeScript generation failed: %v", err)
	}
	if !strings.Contains(tsCode, "export interface User") {
		t.Errorf("TypeScript code missing User interface")
	}
	if !strings.Contains(tsCode, "export interface Task") {
		t.Errorf("TypeScript code missing Task interface")
	}
	if !strings.Contains(tsCode, "async getUsers(") {
		t.Errorf("TypeScript code missing getUsers method")
	}
	if !strings.Contains(tsCode, "async getUsersById(id: string)") {
		t.Errorf("TypeScript code missing getUsersById method")
	}
	if !strings.Contains(tsCode, "async postTasks(body: any)") {
		t.Errorf("TypeScript code missing postTasks method")
	}

	// Test Python
	pyCode, err := g.GeneratePython()
	if err != nil {
		t.Fatalf("Python generation failed: %v", err)
	}
	if !strings.Contains(pyCode, "class User:") {
		t.Errorf("Python code missing User class")
	}
	if !strings.Contains(pyCode, "class Task:") {
		t.Errorf("Python code missing Task class")
	}
	if !strings.Contains(pyCode, "def get_users(self)") {
		t.Errorf("Python code missing get_users method")
	}
	if !strings.Contains(pyCode, "def get_users_by_id(self, id: str)") {
		t.Errorf("Python code missing get_users_by_id method")
	}
	if !strings.Contains(pyCode, "def post_tasks(self, body: Any)") {
		t.Errorf("Python code missing post_tasks method")
	}

	// Test Go
	goCode, err := g.GenerateGo()
	if err != nil {
		t.Fatalf("Go generation failed: %v", err)
	}
	if !strings.Contains(goCode, "type User struct") {
		t.Errorf("Go code missing User struct")
	}
	if !strings.Contains(goCode, "type Task struct") {
		t.Errorf("Go code missing Task struct")
	}
	if !strings.Contains(goCode, "func (c *Client) GetUsers()") {
		t.Errorf("Go code missing GetUsers method")
	}
	if !strings.Contains(goCode, "func (c *Client) GetUsersById(id string)") {
		t.Errorf("Go code missing GetUsersById method")
	}
	if !strings.Contains(goCode, "func (c *Client) PostTasks(body interface{})") {
		t.Errorf("Go code missing PostTasks method")
	}
}

// Wrapper for test accessibility
type testGeneratorWrapper struct {
	*compiler.ClientGenerator
}

func NewClientGeneratorFromProgram(prog *compiler.Program) *testGeneratorWrapper {
	return &testGeneratorWrapper{compiler.NewClientGenerator(prog)}
}

func TestValidationSchemaExtraction(t *testing.T) {
	srvContent := `
route "POST" "/users" (req) {
    let errors = validate(req.body, {
        "name": "required,string",
        "email": "required,email",
        "age": "int"
    })
    return { "success": true }
}
`
	tmpFile := "temp_test_validation.srv"
	if err := os.WriteFile(tmpFile, []byte(srvContent), 0644); err != nil {
		t.Fatalf("Failed to write temporary test file: %v", err)
	}
	defer os.Remove(tmpFile)

	_, prog, err := parseProject(tmpFile)
	if err != nil {
		t.Fatalf("Failed to parse project: %v", err)
	}

	// 1. Test OpenAPI Spec generation
	openAPIJson, err := compiler.GenerateOpenAPI(prog)
	if err != nil {
		t.Fatalf("OpenAPI generation failed: %v", err)
	}

	// Ensure properties and required lists are parsed
	if !strings.Contains(openAPIJson, `"name"`) || !strings.Contains(openAPIJson, `"email"`) || !strings.Contains(openAPIJson, `"age"`) {
		t.Errorf("OpenAPI does not contain validation properties: %s", openAPIJson)
	}
	if !strings.Contains(openAPIJson, `"required"`) || !strings.Contains(openAPIJson, `"integer"`) {
		t.Errorf("OpenAPI missing field constraints: %s", openAPIJson)
	}

	// 2. Test Client SDK generations
	g := NewClientGeneratorFromProgram(prog)

	// TS
	tsCode, err := g.GenerateTypeScript()
	if err != nil {
		t.Fatalf("TS SDK generation failed: %v", err)
	}
	if !strings.Contains(tsCode, "export interface PostUsersRequest") ||
		!strings.Contains(tsCode, "name: string;") ||
		!strings.Contains(tsCode, "age?: number;") ||
		!strings.Contains(tsCode, "postUsers(body: PostUsersRequest)") {
		t.Errorf("TS SDK missing typed validation contract: %s", tsCode)
	}

	// Python
	pyCode, err := g.GeneratePython()
	if err != nil {
		t.Fatalf("Python SDK generation failed: %v", err)
	}
	if !strings.Contains(pyCode, "class PostUsersRequest:") ||
		!strings.Contains(pyCode, "name: str") ||
		!strings.Contains(pyCode, "age: Optional[int]") ||
		!strings.Contains(pyCode, "post_users(self, body: PostUsersRequest)") {
		t.Errorf("Python SDK missing typed validation dataclass: %s", pyCode)
	}

	// Go
	goCode, err := g.GenerateGo()
	if err != nil {
		t.Fatalf("Go SDK generation failed: %v", err)
	}
	if !strings.Contains(goCode, "type PostUsersRequest struct") ||
		!strings.Contains(goCode, "Name string") ||
		!strings.Contains(goCode, "Age *int") ||
		!strings.Contains(goCode, "PostUsers(body PostUsersRequest)") {
		t.Errorf("Go SDK missing typed validation struct: %s", goCode)
	}
}

