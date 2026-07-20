package tabs

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"servconsole/pkg/config"
)

type MigrationEntry struct {
	ID          string    `json:"id"`
	Revision    string    `json:"revision"`
	Description string    `json:"description"`
	Driver      string    `json:"driver"`
	DSN         string    `json:"dsn"`
	SQL         string    `json:"sql"`
	User        string    `json:"user"`
	Timestamp   time.Time `json:"timestamp"`
	Status      string    `json:"status"` // success, failed
	Error       string    `json:"error,omitempty"`
	Delta       string    `json:"delta"`
	DurationMs  int64     `json:"duration_ms"`
}

var (
	Migrations     []MigrationEntry
	MigrationsMu   sync.Mutex
	MigrationsFile = "migrations.json"
)

func LoadMigrations() {
	MigrationsMu.Lock()
	defer MigrationsMu.Unlock()

	data, err := os.ReadFile(MigrationsFile)
	if err == nil {
		_ = json.Unmarshal(data, &Migrations)
	}
	if Migrations == nil {
		Migrations = []MigrationEntry{}
	}
	log.Printf("[migrations] Loaded %d migration audit entries", len(Migrations))
}

func SaveMigrations() {
	data, err := json.MarshalIndent(Migrations, "", "  ")
	if err == nil {
		_ = os.WriteFile(MigrationsFile, data, 0644)
	}
}

func ExtractSchemaDelta(sqlScript string) string {
	var deltas []string
	upper := strings.ToUpper(sqlScript)
	lines := strings.Split(upper, ";")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "CREATE TABLE"):
			deltas = append(deltas, "+ CREATE TABLE")
		case strings.HasPrefix(line, "ALTER TABLE"):
			if strings.Contains(line, "ADD") {
				deltas = append(deltas, "~ ALTER TABLE (ADD column)")
			} else if strings.Contains(line, "DROP") {
				deltas = append(deltas, "~ ALTER TABLE (DROP column)")
			} else if strings.Contains(line, "MODIFY") || strings.Contains(line, "ALTER COLUMN") {
				deltas = append(deltas, "~ ALTER TABLE (MODIFY column)")
			} else if strings.Contains(line, "RENAME") {
				deltas = append(deltas, "~ ALTER TABLE (RENAME)")
			} else {
				deltas = append(deltas, "~ ALTER TABLE")
			}
		case strings.HasPrefix(line, "DROP TABLE"):
			deltas = append(deltas, "- DROP TABLE")
		case strings.HasPrefix(line, "CREATE INDEX") || strings.HasPrefix(line, "CREATE UNIQUE INDEX"):
			deltas = append(deltas, "+ CREATE INDEX")
		case strings.HasPrefix(line, "DROP INDEX"):
			deltas = append(deltas, "- DROP INDEX")
		case strings.HasPrefix(line, "INSERT"):
			deltas = append(deltas, "+ INSERT (seed data)")
		case strings.HasPrefix(line, "UPDATE"):
			deltas = append(deltas, "~ UPDATE (data migration)")
		case strings.HasPrefix(line, "DELETE"):
			deltas = append(deltas, "- DELETE (data cleanup)")
		}
	}
	if len(deltas) == 0 {
		return "SQL script executed"
	}
	return strings.Join(deltas, "; ")
}

func HandleMigrations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		HandleGetMigrations(w, r)
	case http.MethodPost:
		HandleApplyMigration(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func HandleGetMigrations(w http.ResponseWriter, _ *http.Request) {
	MigrationsMu.Lock()
	defer MigrationsMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Migrations)
}

func HandleApplyMigration(w http.ResponseWriter, r *http.Request) {
	if GetUserRole(r) != "admin" {
		WriteJSONError(w, r, "Forbidden: Admin role required to apply migrations", "ERR_FORBIDDEN", http.StatusForbidden)
		return
	}

	var req struct {
		Driver      string `json:"driver"`
		DSN         string `json:"dsn"`
		Revision    string `json:"revision"`
		Description string `json:"description"`
		SQL         string `json:"sql"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteJSONError(w, r, "Invalid request body", "ERR_INVALID_BODY", http.StatusBadRequest)
		return
	}

	if req.Driver == "" || req.DSN == "" || req.Revision == "" || req.SQL == "" {
		WriteJSONError(w, r, "Missing required fields: driver, dsn, revision, sql", "ERR_MISSING_FIELDS", http.StatusBadRequest)
		return
	}

	driver := strings.ToLower(req.Driver)
	dsn := req.DSN
	switch driver {
	case "sqlite", "sqlite3":
		driver = "sqlite"
		dsn = strings.TrimPrefix(dsn, "sqlite://")
	case "postgres", "postgresql":
		driver = "postgres"
	case "mysql":
		driver = "mysql"
	case "oracle":
		driver = "oracle"
	default:
		WriteJSONError(w, r, "Unsupported driver: "+req.Driver, "ERR_UNSUPPORTED_DRIVER", http.StatusBadRequest)
		return
	}

	user := r.Header.Get("X-Console-User")
	if user == "" {
		user = "anonymous"
	}

	migrationID := fmt.Sprintf("mig_%d", time.Now().UnixNano())

	entry := MigrationEntry{
		ID:          migrationID,
		Revision:    req.Revision,
		Description: req.Description,
		Driver:      driver,
		DSN:         config.Redact(dsn),
		SQL:         req.SQL,
		User:        user,
		Timestamp:   time.Now(),
	}

	startTime := time.Now()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		entry.Status = "failed"
		entry.Error = "Connection failed: " + err.Error()
		entry.DurationMs = time.Since(startTime).Milliseconds()
		entry.Delta = "—"
		PersistMigration(entry, user)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entry)
		return
	}
	defer db.Close()

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(15 * time.Second)

	_, err = db.Exec(req.SQL)
	entry.DurationMs = time.Since(startTime).Milliseconds()

	if err != nil {
		entry.Status = "failed"
		entry.Error = err.Error()
		entry.Delta = "—"
	} else {
		entry.Status = "success"
		entry.Delta = ExtractSchemaDelta(req.SQL)
	}

	PersistMigration(entry, user)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entry)
}

func PersistMigration(entry MigrationEntry, user string) {
	MigrationsMu.Lock()
	defer MigrationsMu.Unlock()

	Migrations = append([]MigrationEntry{entry}, Migrations...)
	if len(Migrations) > 500 {
		Migrations = Migrations[:500]
	}
	SaveMigrations()

	status := 200
	if entry.Status == "failed" {
		status = 500
	}
	if AddAuditLog != nil {
		AddAuditLog(user, fmt.Sprintf("Migration %s: %s (rev %s)", entry.Status, entry.Description, entry.Revision), "POST", "/api/db/migrations", status)
	}
}
