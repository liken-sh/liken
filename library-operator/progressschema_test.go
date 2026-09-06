package main

// What these tests read: the progress schema every agent of the
// progress cluster loads, against a real SQLite database. cr-sqlite
// imposes rules the plain database does not enforce, so the tests read
// those out of the loaded database and out of the file's own text.

import (
	"database/sql"
	"os"
	"slices"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// progressSchemaPath is the schema the progress agent loads, from the
// image at /etc/corrosion/progress-schema. The tests read the same file.
const progressSchemaPath = "corrosion/progress-schema/progress.sql"

// newSQLiteProgress opens a database with the shipped progress schema.
// The database name differs from the catalog harness's, so one test can
// hold both at once.
func newSQLiteProgress(t *testing.T) *sql.DB {
	t.Helper()
	// One connection, because an in-memory database belongs to the
	// connection that opened it.
	db, err := sql.Open("sqlite", "file:progress?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	schema, err := os.ReadFile(progressSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	// The driver runs a whole script in one Exec when it binds no
	// parameters, which is how the agent loads this file too.
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("loading the progress schema: %v", err)
	}
	return db
}

// The three tables the store is made of, with the columns the progress
// role writes and reads.
func TestTheProgressSchemaHoldsTheThreeTables(t *testing.T) {
	db := newSQLiteProgress(t)

	cases := []struct {
		table   string
		columns []string
	}{
		{"plays", []string{"play", "player", "library", "watch", "started", "ended",
			"item", "position", "duration", "phase", "season", "episode", "recorded"}},
		{"play_people", []string{"play", "person"}},
		{"play_aliases", []string{"play", "provider", "id"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.table, func(t *testing.T) {
			if got := columnsOf(t, db, testCase.table); !slices.Equal(got, testCase.columns) {
				t.Errorf("%s holds %v, want %v", testCase.table, got, testCase.columns)
			}
		})
	}
}

// The indexes the store's reads take: one person's plays, one Watch's
// plays, and one Player's plays in the order they were recorded.
func TestTheProgressSchemaHoldsTheIndexesTheReadsTake(t *testing.T) {
	db := newSQLiteProgress(t)

	for _, name := range []string{"play_people_person", "plays_watch", "plays_player_recorded"} {
		if !holdsProgressIndex(t, db, name) {
			t.Errorf("the schema holds no index %s", name)
		}
	}
}

// cr-sqlite refuses a table with no primary key and a column that is
// NOT NULL with no default, so a schema that breaks either rule leaves
// the agent serving the old tables and failing every write.
func TestTheProgressSchemaKeepsTheCrSqliteColumnRules(t *testing.T) {
	db := newSQLiteProgress(t)

	for _, table := range []string{"plays", "play_people", "play_aliases"} {
		keys := 0
		rows, err := db.Query(`SELECT name, "notnull", dflt_value, pk FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name string
			var notNull, key int
			var value sql.NullString
			if err := rows.Scan(&name, &notNull, &value, &key); err != nil {
				t.Fatal(err)
			}
			keys += key
			if notNull == 1 && !value.Valid {
				t.Errorf("%s.%s is NOT NULL with no default", table, name)
			}
		}
		rows.Close()
		if keys == 0 {
			t.Errorf("%s has no primary key", table)
		}
	}
}

// cr-sqlite refuses a unique index beyond the primary key, and it reads
// only CREATE TABLE and CREATE INDEX out of a schema file.
func TestTheProgressSchemaStatesNothingCrSqliteRefuses(t *testing.T) {
	db := newSQLiteProgress(t)
	for _, table := range []string{"plays", "play_people", "play_aliases"} {
		rows, err := db.Query(`SELECT name, "unique", origin FROM pragma_index_list(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name, origin string
			var unique int
			if err := rows.Scan(&name, &unique, &origin); err != nil {
				t.Fatal(err)
			}
			if unique == 1 && origin != "pk" {
				t.Errorf("%s carries the unique index %s beyond its primary key", table, name)
			}
		}
		rows.Close()
	}

	body, err := os.ReadFile(progressSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range strings.Split(string(body), ";") {
		trimmed := statementBody(statement)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "CREATE TABLE") && !strings.HasPrefix(trimmed, "CREATE INDEX") {
			t.Errorf("the schema states %q, want a CREATE TABLE or a CREATE INDEX", firstLine(trimmed))
		}
	}
}

// statementBody is one statement of the file with its comment lines and
// its blank lines removed, so a test reads the SQL alone.
func statementBody(statement string) string {
	kept := []string{}
	for _, line := range strings.Split(statement, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, " ")
}

func firstLine(statement string) string {
	line, _, _ := strings.Cut(statement, "\n")
	return line
}

// columnsOf is the columns of one table, in the order the schema
// declares them.
func columnsOf(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

func holdsProgressIndex(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	count := 0
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count == 1
}
