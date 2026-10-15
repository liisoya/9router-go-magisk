//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/db"
)

// newAccountEnv starts a gateway with an empty console buffer, so a case can
// assert on the lines its own requests produced. The buffer is process-wide, so
// clearing it is what separates one case's lines from another's.
func newAccountEnv(t *testing.T) *Env {
	t.Helper()
	env := newEnv(t)
	res := env.Delete(t, "/api/translator/console-logs", WithHeader(auth.CLITokenHeader, auth.CLIToken()))
	if res.Status != http.StatusOK {
		t.Fatalf("console-logs clear status = %d", res.Status)
	}
	return env
}

// A multi-account user has no way to tell which account served a turn: the
// console shows provider+model+tokens and nothing else. Every served request
// must name the account it used.
func TestConsoleLogNamesTheServingAccount(t *testing.T) {
	env := newAccountEnv(t)
	upstream := env.NewUpstream(t, chatCompletionResponder())
	env.AddConnection(t, "conn-deepseek", "deepseek", "DeepSeek Integration", upstream, "sk-upstream")

	res := env.Post(t, "/v1/chat/completions", ChatBody("ds/deepseek-chat", false))
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, body %s", res.Status, truncate(res.Body))
	}

	usageLine := findLogLine(consoleLines(t, env), "usage")
	if usageLine == "" {
		t.Fatal("no usage line in console log")
	}
	if !strings.Contains(usageLine, "connName=DeepSeek Integration") {
		t.Errorf("usage line must name the account that served the request, got %q", usageLine)
	}
	if !strings.Contains(usageLine, "conn=conn-deepseek") {
		t.Errorf("usage line must carry the connection id, got %q", usageLine)
	}
}

// Two connections behind one provider: the log has to say which one answered,
// not just that the provider was used.
func TestConsoleLogDistinguishesAccountsInRotation(t *testing.T) {
	env := newAccountEnv(t)
	upstream := env.NewUpstream(t, chatCompletionResponder())
	env.AddConnection(t, "conn-a", "deepseek", "Account A", upstream, "sk-a")
	env.AddConnection(t, "conn-b", "deepseek", "Account B", upstream, "sk-b")
	if err := env.Repo.SetProviderStrategy("deepseek", db.ProviderStrategy{RotateStrategy: "round-robin", StickyLimit: 1}); err != nil {
		t.Fatalf("set round-robin strategy: %v", err)
	}

	for range 2 {
		res := env.Post(t, "/v1/chat/completions", ChatBody("ds/deepseek-chat", false))
		if res.Status != http.StatusOK {
			t.Fatalf("status = %d, body %s", res.Status, truncate(res.Body))
		}
	}

	usageLines := findLogLines(consoleLines(t, env), "usage")
	if len(usageLines) != 2 {
		t.Fatalf("want one usage line per request, got %d: %q", len(usageLines), usageLines)
	}
	served := map[string]bool{}
	for _, line := range usageLines {
		switch {
		case strings.Contains(line, "connName=Account A"):
			served["A"] = true
		case strings.Contains(line, "connName=Account B"):
			served["B"] = true
		default:
			t.Errorf("usage line must name an account, got %q", line)
		}
	}
	if !served["A"] || !served["B"] {
		t.Errorf("rotation served %v across two accounts, want both; lines %q", served, usageLines)
	}
}

// consoleEntry is the console-logs wire shape: the level and arrival time the
// emitter reported, plus the rendered line. The dashboard colours, filters and
// counts by these fields, so a rename here silently degrades the page to
// one-colour output.
type consoleEntry struct {
	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Line  string    `json:"line"`
}

// consoleLines returns just the rendered lines; use consoleEntries when the
// case asserts on level or timestamp.
func consoleLines(t *testing.T, env *Env) []string {
	t.Helper()
	entries := consoleEntries(t, env)
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Line
	}
	return out
}

func consoleEntries(t *testing.T, env *Env) []consoleEntry {
	t.Helper()
	res := env.Get(t, "/api/translator/console-logs", WithHeader(auth.CLITokenHeader, auth.CLIToken()))
	if res.Status != http.StatusOK {
		t.Fatalf("console-logs status = %d", res.Status)
	}
	var out struct {
		Success bool           `json:"success"`
		Logs    []consoleEntry `json:"logs"`
	}
	res.Decode(t, &out)
	if !out.Success {
		t.Fatalf("console-logs not successful: %s", truncate(res.Body))
	}
	return out.Logs
}

// A failed request is what an operator scans this page for, so it has to arrive
// as level=error. Recovering the level from the message text only worked for
// the four prefixes the logger happens to print; everything else fell through
// to the default colour and the page read as a wall of green.
func TestConsoleLogCarriesEmitterLevel(t *testing.T) {
	env := newAccountEnv(t)
	upstream := env.NewUpstream(t, chatCompletionResponder())
	env.AddConnection(t, "conn-deepseek", "deepseek", "DeepSeek Integration", upstream, "sk-upstream")

	res := env.Post(t, "/v1/chat/completions", ChatBody("ds/deepseek-chat", false))
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, body %s", res.Status, truncate(res.Body))
	}

	entries := consoleEntries(t, env)
	if len(entries) == 0 {
		t.Fatal("console buffer is empty after a served request")
	}
	byLevel := map[string]int{}
	for _, e := range entries {
		if e.Time.IsZero() {
			t.Errorf("entry has no arrival time: %q", e.Line)
		}
		byLevel[e.Level]++
	}
	if byLevel["info"] == 0 {
		t.Errorf("want level=info rows for a served request, got levels %v", byLevel)
	}

	// /v1/messages parses strictly enough to reject a malformed body and logs
	// the rejection through log.Error, unlike the /v1/chat/completions lane
	// which answers 400 without a word.
	badRes := env.Post(t, "/v1/messages", []byte("not-json"))
	if badRes.Status != http.StatusBadRequest {
		t.Fatalf("malformed messages body status = %d, want 400: %s", badRes.Status, truncate(badRes.Body))
	}
	if !hasEntryAtLevel(t, env, "parse JSON failed", "error") {
		t.Error(`no level=error console row for "parse JSON failed"`)
	}
}

// hasEntryAtLevel reports whether the buffer holds a row mentioning substr at
// the given level. It fails on a level mismatch rather than on the search
// alone, so a renamed message surfaces as "no row" instead of passing vacuously.
func hasEntryAtLevel(t *testing.T, env *Env, substr, level string) bool {
	t.Helper()
	for _, e := range consoleEntries(t, env) {
		if !strings.Contains(e.Line, substr) {
			continue
		}
		if e.Level != level {
			t.Errorf("entry %q has level %q, want %q", e.Line, e.Level, level)
		}
		return true
	}
	return false
}

func findLogLine(logs []string, substr string) string {
	lines := findLogLines(logs, substr)
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

func findLogLines(logs []string, substr string) []string {
	var out []string
	for _, line := range logs {
		if strings.Contains(line, substr) {
			out = append(out, line)
		}
	}
	return out
}
