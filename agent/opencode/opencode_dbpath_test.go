package opencode

import (
	"path/filepath"
	"testing"
)

// Regression test for the ghost-session bug: session counts/titles must be
// read from the database file the CLI uses. Reading a different file
// surfaced as ghost sessions with 0 msgs in /list.

// TestLegacyOpencodeDBPath_RespectsXdg pins the location logic.
func TestLegacyOpencodeDBPath_RespectsXdg(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join("C:", "xdg"))
	want := filepath.Join("C:", "xdg", "opencode", "opencode.db")
	if got := legacyOpencodeDBPath(); got != want {
		t.Fatalf("legacy db path = %q, want %q", got, want)
	}
}

// TestQuerySessionMessageCounts_NoDatabase pins that an empty or missing
// database yields no counts without requiring the sqlite3 CLI.
func TestQuerySessionMessageCounts_NoDatabase(t *testing.T) {
	if got := querySessionMessageCounts(""); len(got) != 0 {
		t.Fatalf("counts for empty path = %v, want empty", got)
	}
	missing := filepath.Join(t.TempDir(), "does-not-exist.db")
	if got := querySessionMessageCounts(missing); len(got) != 0 {
		t.Fatalf("counts for missing db = %v, want empty", got)
	}
}

// TestQuerySessionTitle_MissingDB pins that a missing database yields no
// title. The session ID is deliberately bogus so the result is "" in every
// environment (no row can match), without needing any real database.
func TestQuerySessionTitle_MissingDB(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.db")
	if got := querySessionTitle(missing, "ses_doesnotexist"); got != "" {
		t.Fatalf("title for missing db = %q, want empty", got)
	}
}
