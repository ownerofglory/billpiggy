package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestLatestMigrationVersionMatchesTheNewestMigrationFile guards against
// exactly what happened before this test existed: latestMigrationVersion
// silently fell three migrations behind (it named 000015 while 000016,
// 000017, and 000018 already existed on disk), which meant the /readyz
// migrations-applied check stopped verifying anything past 000015 and
// nobody noticed. Bumping it is a one-line change with zero compiler or
// test feedback if forgotten, so this reads the actual migrations
// directory and fails loudly if the constant doesn't name the lexically
// newest *.up.sql file there.
func TestLatestMigrationVersionMatchesTheNewestMigrationFile(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("read migrations directory: %v", err)
	}
	var versions []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if name, ok := strings.CutSuffix(entry.Name(), ".up.sql"); ok {
			versions = append(versions, name)
		}
	}
	if len(versions) == 0 {
		t.Fatal("found no *.up.sql migrations on disk")
	}
	sort.Strings(versions)
	newest := versions[len(versions)-1]
	if latestMigrationVersion != newest {
		t.Fatalf("latestMigrationVersion = %q, want %q (the newest migration file) — bump it in main.go alongside every new migrations/NNNNNN_*.up.sql file", latestMigrationVersion, newest)
	}
}

// TestPostgresPoolConfigSetsLockTimeout guards against the app going fully
// unresponsive without crashing: a connection with no lock_timeout blocks
// forever waiting on a contended row lock, pinning a pool connection until
// the pool has none left for anything else, HTTP requests included. This was
// observed live as two outbox projectors stuck at a fixed pending count
// (readyz correctly reporting them stale) followed by the app no longer
// accepting requests at all.
func TestPostgresPoolConfigSetsLockTimeout(t *testing.T) {
	poolConfig, err := postgresPoolConfig("postgres://user:pass@localhost:5432/billpiggy")
	if err != nil {
		t.Fatalf("postgresPoolConfig: %v", err)
	}
	if got := poolConfig.ConnConfig.RuntimeParams["lock_timeout"]; got != "10s" {
		t.Fatalf("lock_timeout = %q, want %q", got, "10s")
	}
}
