package opencode

import (
	"path/filepath"
	"testing"
)

// Regression tests for the global-/list change: /list must mirror the native
// OpenCode session picker — every project, newest first, forks included —
// instead of only the current work_dir project's top-level sessions.

// TestListOpencodeSessionsGlobal_CoversAllProjects pins that sessions from
// different directories are all listed, newest first.
func TestListOpencodeSessionsGlobal_CoversAllProjects(t *testing.T) {
	path := createTestDB(t,
		[]string{
			testV2MessageSchema,
			`CREATE TABLE session_v2 (id TEXT PRIMARY KEY, title TEXT, directory TEXT, time_created INTEGER, time_updated INTEGER)`,
		},
		[]string{
			`INSERT INTO session_v2 (id, title, directory, time_created, time_updated) VALUES ('ses_old', 'old', 'C:/a', 1000, 1000)`,
			`INSERT INTO session_v2 (id, title, directory, time_created, time_updated) VALUES ('ses_new', 'new', 'C:/b', 2000, 2000)`,
			`INSERT INTO session_v2 (id, title, directory, time_created, time_updated) VALUES ('ses_mid', 'mid', 'C:/a', 1500, 1500)`,
			`INSERT INTO session_message (id, session_id, type) VALUES ('m1', 'ses_new', 'user'), ('m2', 'ses_new', 'assistant')`,
		})
	got, err := listOpencodeSessionsGlobal(path)
	if err != nil {
		t.Fatalf("listOpencodeSessionsGlobal: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("sessions = %d, want 3", len(got))
	}
	if got[0].ID != "ses_new" || got[1].ID != "ses_mid" || got[2].ID != "ses_old" {
		t.Fatalf("order = [%s %s %s], want [ses_new ses_mid ses_old]", got[0].ID, got[1].ID, got[2].ID)
	}
	if got[0].MessageCount != 2 {
		t.Fatalf("ses_new count = %d, want 2", got[0].MessageCount)
	}
	if got[0].Summary != "new" {
		t.Fatalf("ses_new title = %q, want new", got[0].Summary)
	}
}

// TestListOpencodeSessionsGlobal_NoV2Table pins the fallback signal: without
// session_v2 the caller must fall back to the server list.
func TestListOpencodeSessionsGlobal_NoV2Table(t *testing.T) {
	path := createTestDB(t, []string{testV1MessageSchema}, nil)
	if _, err := listOpencodeSessionsGlobal(path); err == nil {
		t.Fatal("expected error for db without session_v2, got nil")
	}
}

// TestListOpencodeSessionsGlobal_MissingDB pins that a missing database
// surfaces an error (so the caller falls back) instead of an empty list.
func TestListOpencodeSessionsGlobal_MissingDB(t *testing.T) {
	if _, err := listOpencodeSessionsGlobal(filepath.Join(t.TempDir(), "does-not-exist.db")); err == nil {
		t.Fatal("expected error for missing db, got nil")
	}
}
