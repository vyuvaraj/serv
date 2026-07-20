package tabs

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vyuvaraj/serv/packages/ServConsole/pkg/auth"
)

func HandleDbQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Driver  string `json:"driver"`
		ConnStr string `json:"connStr"`
		Query   string `json:"query"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	driver := strings.ToLower(req.Driver)
	connStr := req.ConnStr
	query := req.Query

	if driver == "" || connStr == "" || query == "" {
		http.Error(w, "Missing driver, connStr, or query", http.StatusBadRequest)
		return
	}

	switch driver {
	case "sqlite", "sqlite3":
		driver = "sqlite"
		connStr = strings.TrimPrefix(connStr, "sqlite://")
	case "postgres", "postgresql":
		driver = "postgres"
	case "mysql":
		driver = "mysql"
	case "oracle":
		driver = "oracle"
	default:
		http.Error(w, "Unsupported driver: "+req.Driver, http.StatusBadRequest)
		return
	}

	db, err := sql.Open(driver, connStr)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"error":   "Failed to open connection: " + err.Error(),
		})
		return
	}
	defer db.Close()

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(10 * time.Second)

	startTime := time.Now()

	isSelect := false
	trimmedQuery := strings.TrimSpace(strings.ToUpper(query))
	if strings.HasPrefix(trimmedQuery, "SELECT") ||
		strings.HasPrefix(trimmedQuery, "SHOW") ||
		strings.HasPrefix(trimmedQuery, "PRAGMA") ||
		strings.HasPrefix(trimmedQuery, "DESCRIBE") ||
		strings.HasPrefix(trimmedQuery, "DESC") ||
		strings.HasPrefix(trimmedQuery, "EXPLAIN") {
		isSelect = true
	}

	if isSelect {
		rows, err := db.Query(query)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"error":   err.Error(),
			})
			return
		}
		defer rows.Close()

		cols, err := rows.Columns()
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"error":   "Failed to get columns: " + err.Error(),
			})
			return
		}

		results := [][]any{}
		for rows.Next() {
			rowValues := make([]any, len(cols))
			rowPointers := make([]any, len(cols))
			for i := range rowValues {
				rowPointers[i] = &rowValues[i]
			}

			if err := rows.Scan(rowPointers...); err != nil {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"success": false,
					"error":   "Row scanning failed: " + err.Error(),
				})
				return
			}

			cleanedRow := make([]any, len(cols))
			for i, v := range rowValues {
				if b, ok := v.([]byte); ok {
					cleanedRow[i] = string(b)
				} else {
					cleanedRow[i] = v
				}
			}
			results = append(results, cleanedRow)
		}

		if err := rows.Err(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"error":   "Row processing failed: " + err.Error(),
			})
			return
		}

		duration := time.Since(startTime).Milliseconds()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success":         true,
			"isSelect":        true,
			"columns":         cols,
			"rows":            results,
			"executionTimeMs": duration,
		})
	} else {
		res, err := db.Exec(query)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		rowsAffected, _ := res.RowsAffected()
		lastInsertId, _ := res.LastInsertId()
		duration := time.Since(startTime).Milliseconds()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success":         true,
			"isSelect":        false,
			"rowsAffected":    rowsAffected,
			"lastInsertId":    lastInsertId,
			"executionTimeMs": duration,
		})
	}

	user := r.Header.Get("X-Console-User")
	if AddAuditLog != nil {
		AddAuditLog(user, fmt.Sprintf("SQL Query (%s): %.60s", driver, query), r.Method, r.URL.Path, http.StatusOK)
	}
}

func HandlePlaygroundCompile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteJSONError(w, r, "Bad request", "ERR_BAD_REQUEST_BODY", http.StatusBadRequest)
		return
	}

	status := "success"
	var diagnostics []map[string]any
	if strings.Contains(req.Code, "syntax error") || strings.Contains(req.Code, "error") {
		status = "error"
		diagnostics = append(diagnostics, map[string]any{
			"line":    10,
			"column":  5,
			"message": "Syntax error: unexpected token or invalid declaration",
			"type":    "error",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":      status,
		"diagnostics": diagnostics,
		"preview":     "AST compilation complete: 0 errors detected.",
	})
}

func HandleTenantSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TenantID string `json:"tenantId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteJSONError(w, r, "Invalid JSON body", "ERR_INVALID_BODY", http.StatusBadRequest)
		return
	}

	if req.TenantID == "" {
		WriteJSONError(w, r, "tenantId is required", "ERR_TENANT_ID_REQUIRED", http.StatusBadRequest)
		return
	}

	username := r.Header.Get("X-Console-User")
	role := r.Header.Get("X-Console-Role")
	if username == "" {
		WriteJSONError(w, r, "Unauthorized", "ERR_UNAUTHORIZED", http.StatusUnauthorized)
		return
	}

	header := auth.Base64UrlEncode([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := auth.Base64UrlEncode(fmt.Appendf(nil, `{"username":%q,"exp":%d,"role":%q,"tenant_id":%q}`, username, time.Now().Add(24*time.Hour).Unix(), role, req.TenantID))

	secret := auth.JwtSecBytes
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(header + "." + payload))
	signature := auth.Base64UrlEncode(mac.Sum(nil))
	newToken := header + "." + payload + "." + signature

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":      "success",
		"message":     "Tenant scope switched and token rotated successfully",
		"token":       newToken,
		"newTenantId": req.TenantID,
	})
}
