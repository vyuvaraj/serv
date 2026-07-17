package main

import (
	"bytes"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateDryRun(t *testing.T) {
	// 1. Create a temporary workspace directory
	tempDir, err := os.MkdirTemp("", "serv_migrate_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 2. Create a test .srv file defining a table
	srvPath := filepath.Join(tempDir, "schema.srv")
	srvContent := `table users {
		id int @primary @autoincrement
		name string @required
		age int
	}`
	if err := os.WriteFile(srvPath, []byte(srvContent), 0644); err != nil {
		t.Fatalf("failed to write srv file: %v", err)
	}

	// 3. Define temp SQLite database path
	dbPath := filepath.Join(tempDir, "test.db")
	dbConn := "sqlite://" + dbPath

	// 4. Helper to capture stdout during execution
	captureStdout := func(f func()) string {
		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		f()

		w.Close()
		os.Stdout = old

		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		return buf.String()
	}

	// 5. Run runMigrate with dryRun = true (Creation check)
	stdout := captureStdout(func() {
		runMigrate(srvPath, dbConn, false, true)
	})

	// Verify stdout contains the SQL preview
	if !strings.Contains(stdout, "CREATE TABLE IF NOT EXISTS users") {
		t.Errorf("expected dry-run output to contain CREATE TABLE query, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "schema would be applied") {
		t.Errorf("expected stdout to mention 'schema would be applied', got:\n%s", stdout)
	}

	// Verify database remains untouched (table should not exist)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	var tableCount int
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='users'").Scan(&tableCount)
	if err != nil {
		t.Fatalf("failed to check table existence: %v", err)
	}
	if tableCount != 0 {
		t.Error("expected table 'users' to NOT exist after dry-run migration")
	}

	// 6. Apply migration for real (dryRun = false)
	stdout = captureStdout(func() {
		runMigrate(srvPath, dbConn, false, false)
	})
	if !strings.Contains(stdout, "schema applied") {
		t.Errorf("expected stdout to confirm schema applied, got:\n%s", stdout)
	}

	// Verify table now exists
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='users'").Scan(&tableCount)
	if err != nil || tableCount == 0 {
		t.Errorf("expected table 'users' to exist after real migration, err: %v, count: %d", err, tableCount)
	}

	// 7. Update .srv file to add a new column
	srvContentUpdated := `table users {
		id int @primary @autoincrement
		name string @required
		age int
		email string
	}`
	if err := os.WriteFile(srvPath, []byte(srvContentUpdated), 0644); err != nil {
		t.Fatalf("failed to update srv file: %v", err)
	}

	// 8. Run runMigrate with dryRun = true (Altering check)
	stdout = captureStdout(func() {
		runMigrate(srvPath, dbConn, false, true)
	})

	// Verify stdout contains the ALTER TABLE preview
	if !strings.Contains(stdout, "ALTER TABLE users ADD COLUMN email TEXT") {
		t.Errorf("expected dry-run to contain ALTER TABLE query, got:\n%s", stdout)
	}

	// Verify column was NOT actually added to the database yet
	rows, err := db.Query("PRAGMA table_info(users)")
	if err != nil {
		t.Fatalf("failed to get table info: %v", err)
	}
	defer rows.Close()

	hasEmail := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltVal interface{}
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltVal, &pk); err == nil {
			if strings.ToLower(name) == "email" {
				hasEmail = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration failed: %v", err)
	}
	if hasEmail {
		t.Error("expected column 'email' to NOT exist after dry-run alter table")
	}

	// 9. Run rollback with dryRun = true (Rollback check)
	// We restore srv content back to original (without 'email')
	if err := os.WriteFile(srvPath, []byte(srvContent), 0644); err != nil {
		t.Fatalf("failed to restore srv file: %v", err)
	}
	// Add the 'email' column to the database manually so there is a diff to rollback
	if _, err := db.Exec("ALTER TABLE users ADD COLUMN email TEXT"); err != nil {
		t.Fatalf("failed to alter table manually: %v", err)
	}

	stdout = captureStdout(func() {
		runMigrate(srvPath, dbConn, true, true)
	})

	// Verify stdout contains the DROP COLUMN preview
	if !strings.Contains(stdout, "ALTER TABLE users DROP COLUMN email") {
		t.Errorf("expected dry-run rollback to contain DROP COLUMN query, got:\n%s", stdout)
	}
}
