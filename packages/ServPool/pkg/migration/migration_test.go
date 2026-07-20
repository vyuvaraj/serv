package migration

import (
	"testing"
)

func TestParseTablesFromSQLCreate(t *testing.T) {
	sql := "CREATE TABLE users (id INT); CREATE TABLE posts (id INT);"
	created, dropped := ParseTablesFromSQL(sql)
	if len(created) != 2 || created[0] != "users" || created[1] != "posts" {
		t.Errorf("expected created [users posts], got %v", created)
	}
	if len(dropped) != 0 {
		t.Errorf("expected 0 dropped tables, got %d", len(dropped))
	}
}

func TestParseTablesFromSQLDrop(t *testing.T) {
	sql := "DROP TABLE sessions; DROP TABLE tokens;"
	created, dropped := ParseTablesFromSQL(sql)
	if len(created) != 0 {
		t.Errorf("expected 0 created tables, got %d", len(created))
	}
	if len(dropped) != 2 || dropped[0] != "sessions" || dropped[1] != "tokens" {
		t.Errorf("expected dropped [sessions tokens], got %v", dropped)
	}
}

func TestParseTablesFromSQLEmpty(t *testing.T) {
	created, dropped := ParseTablesFromSQL("   ;   ;")
	if len(created) != 0 || len(dropped) != 0 {
		t.Errorf("expected empty lists, got created=%v dropped=%v", created, dropped)
	}
}
