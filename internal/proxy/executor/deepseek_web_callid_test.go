package executor

import (
	"strings"
	"testing"
)

// TestDeepSeekToolCallIDsAreUnique is the regression for the failure the user
// reported twice. Two calls minted in the same turn used to receive the same id,
// because the id was derived from a clock that does not advance between them:

//	first  = "call_shell_969000"
//	second = "call_shell_969000"

// A client that groups parallel calls by id merged them, and the concatenated
// argument objects were rejected before either command ran.
func TestDeepSeekToolCallIDsAreUnique(t *testing.T) {
	const count = 1000
	seen := make(map[string]bool, count)
	for i := 0; i < count; i++ {
		id := deepSeekToolCallID("shell")
		if seen[id] {
			t.Fatalf("id %q was minted twice within one process", id)
		}
		seen[id] = true
	}
}

// TestDeepSeekToolCallIDNamesTheTool keeps the id readable in a log.
func TestDeepSeekToolCallIDNamesTheTool(t *testing.T) {
	if id := deepSeekToolCallID("web_search"); !strings.Contains(id, "web_search") {
		t.Errorf("id = %q, want it to name the tool", id)
	}
}

// TestDeepSeekParallelCallsHaveDistinctIDs is the user-visible case: the two calls
// a "check both cities" answer produces.
func TestDeepSeekParallelCallsHaveDistinctIDs(t *testing.T) {
	calls := deepSeekExtractToolCalls(twoCityAnswer, []deepSeekToolSpec{readTool()})
	if len(calls) != 2 {
		t.Fatalf("extracted %d calls, want 2", len(calls))
	}
	message := deepSeekToolCallMessage(calls, "")
	list, _ := message["tool_calls"].([]any)
	if len(list) != 2 {
		t.Fatalf("message carries %d calls", len(list))
	}
	first := list[0].(map[string]any)["id"]
	second := list[1].(map[string]any)["id"]
	if first == second {
		t.Fatalf("both calls carry id %v; a client keyed by id merges them", first)
	}
}
