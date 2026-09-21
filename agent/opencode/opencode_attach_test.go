package opencode

import (
	"reflect"
	"strings"
	"testing"
)

// TestBuildRunArgs_StandaloneHasNoAttach pins the pre-attach command shape:
// with no serverURL configured, buildRunArgs must remain byte-identical to
// the historical standalone behavior (no --attach anywhere).
func TestBuildRunArgs_StandaloneHasNoAttach(t *testing.T) {
	s := &opencodeSession{workDir: "/repo", model: "provider/model", mode: "default"}

	got := s.buildRunArgs("hello", nil, "")
	want := []string{"run", "--format", "json", "--model", "provider/model", "--dir", "/repo", "--thinking"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("standalone args = %#v, want %#v", got, want)
	}
	for _, a := range got {
		if a == "--attach" {
			t.Fatalf("standalone args unexpectedly contain --attach: %#v", got)
		}
	}
}

// TestBuildRunArgs_AttachPrependsServerURL verifies that a configured
// serverURL injects exactly ["--attach", url] right after
// ["run", "--format", "json"], with all existing args preserved after it.
func TestBuildRunArgs_AttachPrependsServerURL(t *testing.T) {
	s := &opencodeSession{
		workDir:   "/repo",
		model:     "provider/model",
		serverURL: "http://127.0.0.1:4096",
	}

	got := s.buildRunArgs("hello", nil, "ses_123")
	want := []string{
		"run", "--format", "json",
		"--attach", "http://127.0.0.1:4096",
		"--session", "ses_123",
		"--model", "provider/model",
		"--dir", "/repo",
		"--thinking",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attach args = %#v, want %#v", got, want)
	}
}

// TestBuildRunArgs_AttachPreservesAllOptions ensures attach mode keeps every
// existing behavior: agent selection, yolo permissions, work_dir, files,
// session resume, and extra cmd args.
func TestBuildRunArgs_AttachPreservesAllOptions(t *testing.T) {
	s := &opencodeSession{
		extraArgs: []string{"--log-level", "ERROR"},
		workDir:   "/repo",
		model:     "m",
		mode:      "yolo",
		agentName: "build",
		serverURL: "http://127.0.0.1:4096",
	}

	got := s.buildRunArgs("hi", []string{"/tmp/a.png"}, "ses_9")
	want := []string{
		"--log-level", "ERROR",
		"run", "--format", "json",
		"--attach", "http://127.0.0.1:4096",
		"--session", "ses_9",
		"--agent", "build",
		"--model", "m",
		"--dir", "/repo",
		"--thinking",
		"--dangerously-skip-permissions",
		"--file", "/tmp/a.png",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attach args = %#v, want %#v", got, want)
	}
}

// TestBuildRunArgs_WorkDirPreservedInBothModes verifies --dir scoping is
// identical in standalone and attach mode: the server interprets --dir on
// its own side when attaching, so per-project work_dir isolation holds.
func TestBuildRunArgs_WorkDirPreservedInBothModes(t *testing.T) {
	for _, dir := range []string{"/repo-a", "/repo-b"} {
		standalone := (&opencodeSession{workDir: dir}).buildRunArgs("hi", nil, "")
		attached := (&opencodeSession{workDir: dir, serverURL: "http://127.0.0.1:4096"}).buildRunArgs("hi", nil, "")

		// attached = standalone with exactly ["--attach", url] inserted at index 3.
		want := append(append([]string{}, standalone[:3]...),
			append([]string{"--attach", "http://127.0.0.1:4096"}, standalone[3:]...)...)
		if !reflect.DeepEqual(attached, want) {
			t.Fatalf("dir %s: attached = %#v, want %#v", dir, attached, want)
		}
		if got := attached[len(attached)-3]; got != "--dir" {
			t.Fatalf("dir %s: missing --dir flag: %#v", dir, attached)
		}
		if got := attached[len(attached)-2]; got != dir {
			t.Fatalf("dir %s: --dir = %q, want %q", dir, got, dir)
		}
	}
}

// TestNormalizeServerURL covers config parsing: absent/empty stays
// standalone, whitespace is trimmed, non-http(s) fails fast.
func TestNormalizeServerURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     any
		want    string
		wantErr bool
	}{
		{"absent", nil, "", false},
		{"empty", "", "", false},
		{"blank", "   ", "", false},
		{"non-string", 42, "", false},
		{"http", "http://127.0.0.1:4096", "http://127.0.0.1:4096", false},
		{"https", "https://opencode.internal:4096", "https://opencode.internal:4096", false},
		{"trims-spaces", "  http://127.0.0.1:4096  ", "http://127.0.0.1:4096", false},
		{"bare-host", "127.0.0.1:4096", "", true},
		{"wrong-scheme", "ws://127.0.0.1:4096", "", true},
		{"garbage", "not a url", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeServerURL(tc.raw)
			if tc.wantErr && err == nil {
				t.Fatalf("normalizeServerURL(%v) err = nil, want error", tc.raw)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("normalizeServerURL(%v) err = %v, want nil", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("normalizeServerURL(%v) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestSanitizeServerURLForLog ensures logs never expose credentials even if
// userinfo is embedded in a misconfigured URL.
func TestSanitizeServerURLForLog(t *testing.T) {
	if got := sanitizeServerURLForLog("http://127.0.0.1:4096"); got != "http://127.0.0.1:4096" {
		t.Fatalf("plain URL rewritten: %q", got)
	}
	got := sanitizeServerURLForLog("http://user:s3cret@127.0.0.1:4096")
	if strings.Contains(got, "s3cret") || strings.Contains(got, "user:") {
		t.Fatalf("credentials leaked in sanitized URL: %q", got)
	}
	if !strings.Contains(got, "127.0.0.1:4096") {
		t.Fatalf("host lost in sanitized URL: %q", got)
	}
}

// TestAttachErrMsg verifies standalone errors pass through untouched while
// attach errors are prefixed with the sanitized server URL (no silent
// fallback: the backend failure stays visible and attributable).
func TestAttachErrMsg(t *testing.T) {
	if got := attachErrMsg("", "boom"); got != "boom" {
		t.Fatalf("standalone err = %q, want passthrough", got)
	}
	got := attachErrMsg("http://127.0.0.1:4096", "boom")
	if !strings.Contains(got, "http://127.0.0.1:4096") || !strings.Contains(got, "boom") {
		t.Fatalf("attach err = %q, want server + message", got)
	}
	got = attachErrMsg("http://user:pw@host:4096", "boom")
	if strings.Contains(got, "pw") {
		t.Fatalf("attach err leaks credentials: %q", got)
	}
}

// TestAttach_ConversationIsolation verifies the key attach invariant: reusing
// the same backend (same serverURL) does NOT merge conversations. Session
// identity still comes from chatID via --session, exactly as in standalone.
func TestAttach_ConversationIsolation(t *testing.T) {
	const server = "http://127.0.0.1:4096"

	argsFor := func(chatID string) []string {
		return (&opencodeSession{workDir: "/repo", serverURL: server}).buildRunArgs("hi", nil, chatID)
	}

	// Same conversation, repeated messages: same --session (semantics
	// preserved from standalone).
	if a, b := argsFor("ses_A"), argsFor("ses_A"); !reflect.DeepEqual(a, b) {
		t.Fatalf("same conversation diverged:\n%#v\n%#v", a, b)
	}

	// Two unrelated conversations: different --session despite shared backend.
	a, b := argsFor("ses_A"), argsFor("ses_B")
	var sessionOf = func(args []string) string {
		for i, v := range args {
			if v == "--session" && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	if sessionOf(a) != "ses_A" || sessionOf(b) != "ses_B" {
		t.Fatalf("session mapping broken: %#v vs %#v", a, b)
	}

	// Fresh conversation (no chatID yet): no --session, same as standalone.
	if got := argsFor(""); sessionOf(got) != "" {
		t.Fatalf("fresh conversation should have no --session: %#v", got)
	}
}

// TestWorkspaceAgentOptions_PreservesServerURL is a regression test for the
// multi-workspace propagation gap: server_url must survive the per-workspace
// agent copy in core.Engine.getOrCreateWorkspaceAgent, otherwise workspaces
// would silently split between attached and standalone backends.
func TestWorkspaceAgentOptions_PreservesServerURL(t *testing.T) {
	a := &Agent{mode: "default", serverURL: "http://127.0.0.1:4096"}
	opts := a.WorkspaceAgentOptions()
	if opts["server_url"] != "http://127.0.0.1:4096" {
		t.Fatalf("server_url = %v, want http://127.0.0.1:4096", opts["server_url"])
	}

	plain := (&Agent{mode: "default"}).WorkspaceAgentOptions()
	if _, ok := plain["server_url"]; ok {
		t.Fatalf("standalone opts unexpectedly contain server_url: %v", plain)
	}
}
