//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/samber/lo"
)

// connectionRow is the sanitized shape GET /api/connections returns. The
// dashboard reads it; the important part is that apiKey never appears, since
// the response is reachable by any client holding an API key.
type connectionRow struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	AuthType string `json:"authType"`
	Name     string `json:"name"`
	IsActive int    `json:"isActive"`
	Priority int    `json:"priority"`
}

// listConnections fetches every connection the dashboard can see.
func listConnections(t *testing.T, env *Env) []connectionRow {
	t.Helper()
	res := env.Get(t, "/api/connections")
	if res.Status != http.StatusOK {
		t.Fatalf("GET /api/connections = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	var rows []connectionRow
	res.Decode(t, &rows)
	return rows
}

// requireConnection returns the row with the given id, failing when it is
// absent. Every caller asserts on one specific row, so a list that came back
// empty (or without it) has to be a failure rather than a silently skipped loop.
func requireConnection(t *testing.T, rows []connectionRow, id string) connectionRow {
	t.Helper()
	row, ok := lo.Find(rows, func(r connectionRow) bool { return r.ID == id })
	if !ok {
		t.Fatalf("connection %s is not in the list %+v", id, rows)
	}
	return row
}

// TestConnectionLifecycle drives the full dashboard CRUD cycle for a provider
// account over HTTP: create, list, rename, delete. This is the path an operator
// uses on first run, and each step is where a broken JSON tag or a dropped route
// would otherwise go unnoticed.
func TestConnectionLifecycle(t *testing.T) {
	env := newEnv(t)

	created := env.Post(t, "/api/connections", map[string]any{
		"provider": "deepseek",
		"authType": "apikey",
		"name":     "DeepSeek Primary",
		"apiKey":   "sk-deepseek-secret",
		"priority": 1,
	})
	if created.Status != http.StatusOK {
		t.Fatalf("POST /api/connections = %d, want 200 (body: %s)", created.Status, truncate(created.Body))
	}
	var createResp struct {
		Status string `json:"status"`
		ID     string `json:"id"`
	}
	created.Decode(t, &createResp)
	if createResp.Status != "ok" || createResp.ID == "" {
		t.Fatalf("POST /api/connections response = %s, want status ok and an id", truncate(created.Body))
	}
	id := createResp.ID

	t.Run("listed with its secret withheld", func(t *testing.T) {
		found := requireConnection(t, listConnections(t, env), id)
		if found.Provider != "deepseek" || found.Name != "DeepSeek Primary" {
			t.Errorf("listed connection = %+v, want the deepseek account just created", found)
		}
		if found.IsActive != 1 {
			t.Errorf("isActive = %d, want 1 for a freshly created connection", found.IsActive)
		}
		if found.Priority != 1 {
			t.Errorf("priority = %d, want the priority the create sent", found.Priority)
		}

		// The whole response, not just this row, must be free of secrets.
		res := env.Get(t, "/api/connections")
		if strings.Contains(string(res.Body), "sk-deepseek-secret") {
			t.Errorf("GET /api/connections leaked the api key: %s", truncate(res.Body))
		}
	})

	t.Run("update renames it", func(t *testing.T) {
		res := env.Put(t, "/api/connections/"+id, map[string]any{
			"provider": "deepseek",
			"authType": "apikey",
			"name":     "DeepSeek Renamed",
			"apiKey":   "sk-deepseek-secret",
			"priority": 1,
		})
		if res.Status != http.StatusOK {
			t.Fatalf("PUT /api/connections/%s = %d, want 200 (body: %s)", id, res.Status, truncate(res.Body))
		}
		if got := requireConnection(t, listConnections(t, env), id).Name; got != "DeepSeek Renamed" {
			t.Errorf("connection name = %q, want \"DeepSeek Renamed\"", got)
		}
	})

	t.Run("delete removes it", func(t *testing.T) {
		res := env.Delete(t, "/api/connections/"+id)
		if res.Status != http.StatusOK {
			t.Fatalf("DELETE /api/connections/%s = %d, want 200 (body: %s)", id, res.Status, truncate(res.Body))
		}
		remaining := listConnections(t, env)
		if lo.ContainsBy(remaining, func(r connectionRow) bool { return r.ID == id }) {
			t.Errorf("connection %s still listed after DELETE", id)
		}
	})
}

// TestConnectionNameConflictIsRefused pins the duplicate-name guard: a create
// that reuses a name must be refused with a typed 409, never silently replace
// the stored credential. Scripts that name rows "Key 1", "Key 2" depend on the
// refusal to notice they overwrote an account.
func TestConnectionNameConflictIsRefused(t *testing.T) {
	env := newEnv(t)

	first := env.Post(t, "/api/connections", map[string]any{
		"provider": "deepseek", "authType": "apikey", "name": "Primary", "apiKey": "sk-first",
	})
	if first.Status != http.StatusOK {
		t.Fatalf("first POST /api/connections = %d, want 200 (body: %s)", first.Status, truncate(first.Body))
	}
	var firstResp struct {
		ID string `json:"id"`
	}
	first.Decode(t, &firstResp)

	t.Run("duplicate name is a 409", func(t *testing.T) {
		second := env.Post(t, "/api/connections", map[string]any{
			"provider": "deepseek", "authType": "apikey", "name": "Primary", "apiKey": "sk-second",
		})
		if second.Status != http.StatusConflict {
			t.Fatalf("duplicate POST /api/connections = %d, want 409 (body: %s)", second.Status, truncate(second.Body))
		}
		var conflict struct {
			Code     string `json:"code"`
			Existing string `json:"existingId"`
		}
		second.Decode(t, &conflict)
		if conflict.Code != "PROVIDER_NAME_CONFLICT" {
			t.Errorf("conflict code = %q, want PROVIDER_NAME_CONFLICT", conflict.Code)
		}
		if conflict.Existing != firstResp.ID {
			t.Errorf("conflict existingId = %q, want %q", conflict.Existing, firstResp.ID)
		}
	})

	t.Run("allowOverwrite replaces in place", func(t *testing.T) {
		res := env.Post(t, "/api/connections", map[string]any{
			"provider": "deepseek", "authType": "apikey", "name": "Primary",
			"apiKey": "sk-second", "allowOverwrite": true,
		})
		if res.Status != http.StatusOK {
			t.Fatalf("overwrite POST /api/connections = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
		}
		rows := listConnections(t, env)
		requireConnection(t, rows, firstResp.ID)
		count := lo.CountBy(rows, func(r connectionRow) bool { return r.Provider == "deepseek" })
		if count != 1 {
			t.Errorf("deepseek connections = %d, want 1: an overwrite must replace, not append", count)
		}
	})
}

// TestConnectionValidationRejectsAnIncompletePayload pins the create-time
// validation so a typo in the dashboard cannot store a half-configured account
// that only fails much later, on a user's live request.
func TestConnectionValidationRejectsAnIncompletePayload(t *testing.T) {
	env := newEnv(t)

	res := env.Post(t, "/api/connections", map[string]any{"name": "No Provider"})
	if res.Status != http.StatusBadRequest {
		t.Fatalf("POST /api/connections without a provider = %d, want 400 (body: %s)", res.Status, truncate(res.Body))
	}
	if msg := res.ErrorMessage(t); !strings.Contains(msg, "provider") {
		t.Errorf("error message = %q, want it to name the missing provider", msg)
	}

	if rows := listConnections(t, env); len(rows) != 0 {
		t.Errorf("stored %d connections after a rejected create, want 0", len(rows))
	}
}
