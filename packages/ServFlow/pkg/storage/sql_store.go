package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	"github.com/vyuvaraj/ServShared"
	_ "modernc.org/sqlite"
)

type SQLWorkflowStore struct {
	db     *sql.DB
	driver string
}

func NewSQLWorkflowStore(driver, dsn string) (*SQLWorkflowStore, error) {
	// Standardize driver name
	drv := strings.ToLower(driver)
	if drv == "postgres" || drv == "postgresql" {
		drv = "postgres"
	}

	db, err := sql.Open(drv, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	store := &SQLWorkflowStore{
		db:     db,
		driver: drv,
	}

	if err := store.bootstrap(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to bootstrap database tables: %w", err)
	}

	return store, nil
}

func (s *SQLWorkflowStore) Close() error {
	return s.db.Close()
}

func (s *SQLWorkflowStore) bootstrap() error {
	var createDefsTable string
	var createInstsTable string

	switch s.driver {
	case "postgres":
		createDefsTable = `CREATE TABLE IF NOT EXISTS workflow_definitions (
			id VARCHAR(255) PRIMARY KEY,
			data TEXT NOT NULL
		);`
		createInstsTable = `CREATE TABLE IF NOT EXISTS workflow_instances (
			id VARCHAR(255) PRIMARY KEY,
			workflow_id VARCHAR(255) NOT NULL,
			status VARCHAR(50) NOT NULL,
			data TEXT NOT NULL
		);`
	case "mysql":
		createDefsTable = `CREATE TABLE IF NOT EXISTS workflow_definitions (
			id VARCHAR(255) PRIMARY KEY,
			data LONGTEXT NOT NULL
		);`
		createInstsTable = `CREATE TABLE IF NOT EXISTS workflow_instances (
			id VARCHAR(255) PRIMARY KEY,
			workflow_id VARCHAR(255) NOT NULL,
			status VARCHAR(50) NOT NULL,
			data LONGTEXT NOT NULL
		);`
	default: // sqlite
		createDefsTable = `CREATE TABLE IF NOT EXISTS workflow_definitions (
			id TEXT PRIMARY KEY,
			data TEXT NOT NULL
		);`
		createInstsTable = `CREATE TABLE IF NOT EXISTS workflow_instances (
			id TEXT PRIMARY KEY,
			workflow_id TEXT NOT NULL,
			status TEXT NOT NULL,
			data TEXT NOT NULL
		);`
	}

	if _, err := s.db.Exec(createDefsTable); err != nil {
		return err
	}
	if _, err := s.db.Exec(createInstsTable); err != nil {
		return err
	}
	return nil
}

func (s *SQLWorkflowStore) GetClient() *ServShared.StoreClient {
	return nil
}

func (s *SQLWorkflowStore) LoadDefinitions() (map[string]WorkflowDef, error) {
	rows, err := s.db.Query("SELECT id, data FROM workflow_definitions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	defs := make(map[string]WorkflowDef)
	for rows.Next() {
		var id string
		var rawData string
		if err := rows.Scan(&id, &rawData); err != nil {
			return nil, err
		}
		var def WorkflowDef
		if err := json.Unmarshal([]byte(rawData), &def); err != nil {
			return nil, err
		}
		defs[id] = def
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	log.Printf("[PERSISTENCE] Loaded %d workflow definitions from SQL (%s)", len(defs), s.driver)
	return defs, nil
}

func (s *SQLWorkflowStore) SaveDefinitions(defs map[string]WorkflowDef) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear existing definitions
	if _, err := tx.Exec("DELETE FROM workflow_definitions"); err != nil {
		return err
	}

	insertQuery := "INSERT INTO workflow_definitions (id, data) VALUES ($1, $2)"
	if s.driver == "mysql" {
		insertQuery = "INSERT INTO workflow_definitions (id, data) VALUES (?, ?)"
	}

	for id, def := range defs {
		data, err := json.Marshal(def)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(insertQuery, id, string(data)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *SQLWorkflowStore) LoadInstances() (map[string]*WorkflowInstance, error) {
	rows, err := s.db.Query("SELECT id, data FROM workflow_instances")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	insts := make(map[string]*WorkflowInstance)
	for rows.Next() {
		var id string
		var rawData string
		if err := rows.Scan(&id, &rawData); err != nil {
			return nil, err
		}
		var inst WorkflowInstance
		if err := json.Unmarshal([]byte(rawData), &inst); err != nil {
			return nil, err
		}
		insts[id] = &inst
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	log.Printf("[PERSISTENCE] Loaded %d workflow instances from SQL (%s)", len(insts), s.driver)
	return insts, nil
}

func (s *SQLWorkflowStore) SaveInstances(insts map[string]*WorkflowInstance) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear existing instances
	if _, err := tx.Exec("DELETE FROM workflow_instances"); err != nil {
		return err
	}

	insertQuery := "INSERT INTO workflow_instances (id, workflow_id, status, data) VALUES ($1, $2, $3, $4)"
	if s.driver == "mysql" {
		insertQuery = "INSERT INTO workflow_instances (id, workflow_id, status, data) VALUES (?, ?, ?, ?)"
	}

	for id, inst := range insts {
		inst.Mu.RLock()
		data, err := json.Marshal(inst)
		inst.Mu.RUnlock()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(insertQuery, id, inst.WorkflowID, inst.Status, string(data)); err != nil {
			return err
		}
	}

	return tx.Commit()
}
