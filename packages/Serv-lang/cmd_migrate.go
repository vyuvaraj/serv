package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vyuvaraj/serv/packages/Serv-lang/compiler"

	// SQLite driver
	_ "github.com/glebarez/go-sqlite"
)

// runMigrate parses all .srv files in the target path, extracts TableDecl nodes,
// compares them against the live database schema, and applies any missing tables
// or columns as a structural migration.
//
// Usage: serv migrate [file-or-dir] [--db <connection-string>]
// Usage: serv migrate [file-or-dir] [--db <connection-string>] [--rollback]
// Usage: serv migrate [file-or-dir] [--db <connection-string>]
// Usage: serv migrate [file-or-dir] [--db <connection-string>] [--rollback]
// Usage: serv migrate [file-or-dir] [--db <connection-string>] [--dry-run]
func runMigrate(target string, dbConn string, rollback bool, dryRun bool) {
	if target == "" {
		target = "."
	}

	// Resolve .srv source files
	srvFiles, err := findSrvFiles(target)
	if err != nil {
		fmt.Printf("Error finding .srv files: %v\n", err)
		os.Exit(1)
	}
	if len(srvFiles) == 0 {
		fmt.Println("No .srv files found.")
		return
	}

	// Parse all files and collect TableDecl nodes
	tables := collectTableDecls(srvFiles)
	if len(tables) == 0 {
		fmt.Println("No declarative table schemas found. Use `table <name> { ... }` to define schemas.")
		return
	}

	fmt.Printf("Found %d table declaration(s):\n", len(tables))
	for _, t := range tables {
		fmt.Printf("  • %s (%d columns)\n", t.Name, len(t.Columns))
	}
	fmt.Println()

	// Open database
	if dbConn == "" {
		dbConn = os.Getenv("DATABASE_URL")
	}
	if dbConn == "" {
		dbConn = "sqlite://serv.db"
	}

	db, err := openDB(dbConn)
	if err != nil {
		fmt.Printf("Failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Ensure migration tracking table exists (only if not dry-run)
	if !dryRun {
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS serv_schema_migrations (
			table_name TEXT PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
			fmt.Printf("Failed to create migration tracking table: %v\n", err)
			os.Exit(1)
		}
	}

	if rollback {
		if dryRun {
			fmt.Println("\033[33m🔍 Dry-run: Reverting/rolling back schema modifications (preview):\033[0m")
		} else {
			fmt.Println("🔄 Rolling back schema changes...")
		}
		rolledBack := 0
		for _, td := range tables {
			changed, err := rollbackTableDecl(db, td, dryRun)
			if err != nil {
				fmt.Printf("  ✗ %s rollback failed: %v\n", td.Name, err)
				continue
			}
			if changed {
				rolledBack++
				if dryRun {
					fmt.Printf("  ✓ %s: schema modifications would be rolled back\n", td.Name)
				} else {
					fmt.Printf("  ✓ %s: rolled back schema modifications\n", td.Name)
				}
			} else {
				fmt.Printf("  - %s: no rollback actions needed\n", td.Name)
			}
		}
		if rolledBack > 0 {
			if dryRun {
				fmt.Printf("\033[33mDry-run complete: %d table(s) would be updated/reverted.\033[0m\n", rolledBack)
			} else {
				fmt.Printf("Rollback complete: %d table(s) updated/reverted.\n", rolledBack)
			}
		} else {
			fmt.Println("No rollback actions performed.")
		}
		return
	}

	if dryRun {
		fmt.Println("\033[33m🔍 Dry-run: Applying schema migrations (preview):\033[0m")
	}

	// Apply each table declaration
	applied := 0
	for _, td := range tables {
		changed, err := applyTableDecl(db, td, dryRun)
		if err != nil {
			fmt.Printf("  ✗ %s: %v\n", td.Name, err)
			continue
		}
		if changed {
			applied++
			if dryRun {
				fmt.Printf("  ✓ %s: schema would be applied\n", td.Name)
			} else {
				fmt.Printf("  ✓ %s: schema applied\n", td.Name)
			}
		} else {
			fmt.Printf("  - %s: already up to date\n", td.Name)
		}
	}

	fmt.Println()
	if dryRun {
		if applied > 0 {
			fmt.Printf("\033[33mDry-run complete. %d table(s) would be created/updated.\033[0m\n", applied)
		} else {
			fmt.Println("Database schema is already up to date (no actions needed).")
		}
	} else {
		if applied > 0 {
			fmt.Printf("Migration complete: %d table(s) created/updated.\n", applied)
		} else {
			fmt.Println("Database schema is already up to date.")
		}
	}
}

// rollbackTableDecl reverts columns that are in the database but NOT in the TableDecl.
func rollbackTableDecl(db *sql.DB, td *compiler.TableDecl, dryRun bool) (bool, error) {
	existingCols, err := getExistingColumns(db, td.Name)
	if err != nil {
		// Table might not exist at all, skip
		return false, nil
	}

	declaredCols := make(map[string]bool)
	for _, col := range td.Columns {
		declaredCols[strings.ToLower(col.Name)] = true
	}

	rolledBack := false
	for colName := range existingCols {
		// If column is in db but NOT declared in srv, drop it (rollback)
		if !declaredCols[colName] {
			dropSQL := fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", td.Name, colName)
			if dryRun {
				printDryRunSQL(dropSQL, false)
				rolledBack = true
			} else {
				if _, err := db.Exec(dropSQL); err != nil {
					// Fallback: if DROP COLUMN is unsupported by old engine version, continue
					continue
				}
				rolledBack = true
			}
		}
	}
	return rolledBack, nil
}

// applyTableDecl checks if the table exists in the DB and creates or alters it.
// Returns true if any SQL was executed.
func applyTableDecl(db *sql.DB, td *compiler.TableDecl, dryRun bool) (bool, error) {
	// Check if table exists (SQLite)
	var count int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", td.Name,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("schema check failed: %w", err)
	}

	if count == 0 {
		// Table doesn't exist — generate CREATE TABLE
		createSQL := tableToSQL(td)
		if dryRun {
			printDryRunSQL(createSQL, true)
			return true, nil
		}
		if _, err := db.Exec(createSQL); err != nil {
			return false, fmt.Errorf("CREATE TABLE failed: %w", err)
		}
		return true, nil
	}

	// Table exists — check for missing columns (ALTER TABLE ADD COLUMN)
	existingCols, err := getExistingColumns(db, td.Name)
	if err != nil {
		return false, err
	}

	altered := false
	for _, col := range td.Columns {
		if !existingCols[strings.ToLower(col.Name)] {
			alterSQL := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s",
				td.Name, col.Name, servTypeToSQLMigrate(col.Type))
			if col.Default != nil {
				defVal := *col.Default
				if defVal == "now" {
					alterSQL += " DEFAULT CURRENT_TIMESTAMP"
				} else if col.Type == "string" || col.Type == "datetime" {
					alterSQL += " DEFAULT '" + defVal + "'"
				} else {
					alterSQL += " DEFAULT " + defVal
				}
			}
			if dryRun {
				printDryRunSQL(alterSQL, true)
				altered = true
			} else {
				if _, err := db.Exec(alterSQL); err != nil {
					return false, fmt.Errorf("ALTER TABLE failed for column %s: %w", col.Name, err)
				}
				fmt.Printf("    + added column %s.%s\n", td.Name, col.Name)
				altered = true
			}
		}
	}
	return altered, nil
}

func printDryRunSQL(sqlStr string, isAdd bool) {
	lines := strings.Split(sqlStr, "\n")
	prefix := "+"
	color := "\033[32m" // Green
	if !isAdd {
		prefix = "-"
		color = "\033[31m" // Red
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fmt.Printf("%s%s %s\033[0m\n", color, prefix, line)
	}
}

// getExistingColumns returns a set of lowercase column names for a table.
func getExistingColumns(db *sql.DB, tableName string) (map[string]bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltVal interface{}
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltVal, &pk); err != nil {
			continue
		}
		cols[strings.ToLower(name)] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cols, nil
}

// tableToSQL generates a CREATE TABLE IF NOT EXISTS statement from a TableDecl.
// Standalone version used by the migrate command (mirrors codegen logic).
func tableToSQL(td *compiler.TableDecl) string {
	var sb strings.Builder
	sb.WriteString("CREATE TABLE IF NOT EXISTS ")
	sb.WriteString(td.Name)
	sb.WriteString(" (\n")

	for i, col := range td.Columns {
		sb.WriteString("    ")
		sb.WriteString(col.Name)
		sb.WriteString(" ")
		sb.WriteString(servTypeToSQLMigrate(col.Type))

		if col.Primary {
			sb.WriteString(" PRIMARY KEY")
		}
		if col.AutoIncrement {
			sb.WriteString(" AUTOINCREMENT")
		}
		if col.Required {
			sb.WriteString(" NOT NULL")
		}
		if col.Unique {
			sb.WriteString(" UNIQUE")
		}
		if col.Default != nil {
			defVal := *col.Default
			if defVal == "now" {
				sb.WriteString(" DEFAULT CURRENT_TIMESTAMP")
			} else if col.Type == "string" || col.Type == "datetime" {
				sb.WriteString(" DEFAULT '")
				sb.WriteString(defVal)
				sb.WriteString("'")
			} else {
				sb.WriteString(" DEFAULT ")
				sb.WriteString(defVal)
			}
		}
		if i < len(td.Columns)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString(")")
	return sb.String()
}

func servTypeToSQLMigrate(t string) string {
	switch strings.ToLower(t) {
	case "int":
		return "INTEGER"
	case "float", "float64":
		return "REAL"
	case "bool":
		return "INTEGER"
	case "datetime":
		return "DATETIME"
	default:
		return "TEXT"
	}
}

// collectTableDecls parses the given .srv files and returns all TableDecl nodes.
func collectTableDecls(files []string) []*compiler.TableDecl {
	var tables []*compiler.TableDecl
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		lexer := compiler.NewLexer(string(src))
		parser := compiler.NewParser(lexer)
		program := parser.ParseProgram()
		for _, stmt := range program.Statements {
			if td, ok := stmt.(*compiler.TableDecl); ok {
				tables = append(tables, td)
			}
		}
	}
	return tables
}

// findSrvFiles returns all .srv files under the given path (file or directory).
func findSrvFiles(target string) ([]string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{target}, nil
	}

	var files []string
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".srv" {
			files = append(files, filepath.Join(target, entry.Name()))
		}
	}
	return files, nil
}

// openDB opens a database connection from a connection string.
// Supports sqlite://, postgres://, mysql:// prefixes.
func openDB(connStr string) (*sql.DB, error) {
	if strings.HasPrefix(connStr, "sqlite://") {
		path := strings.TrimPrefix(connStr, "sqlite://")
		return sql.Open("sqlite", path)
	}
	if strings.HasPrefix(connStr, "postgres://") || strings.HasPrefix(connStr, "postgresql://") {
		return sql.Open("postgres", connStr)
	}
	if strings.HasPrefix(connStr, "mysql://") {
		dsn := strings.TrimPrefix(connStr, "mysql://")
		return sql.Open("mysql", dsn)
	}
	// Default: treat as SQLite file path
	return sql.Open("sqlite", connStr)
}
