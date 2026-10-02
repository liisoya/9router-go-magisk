//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/samber/lo"
)

// modelEntry is one advertised model in the /v1/models list.
type modelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

// modelsResponse mirrors the /v1/models envelope: OpenAI's "data" array plus the
// extra keys the dashboard and CLI tools read.
type modelsResponse struct {
	Object      string       `json:"object"`
	Connections int          `json:"connections"`
	Data        []modelEntry `json:"data"`
}

// ids flattens the model ids for order-sensitive assertions.
func (m modelsResponse) ids() []string {
	return lo.Map(m.Data, func(entry modelEntry, _ int) string { return entry.ID })
}

// hasPrefix reports whether any advertised model id starts with prefix.
func (m modelsResponse) hasPrefix(prefix string) bool {
	return lo.ContainsBy(m.ids(), func(id string) bool { return strings.HasPrefix(id, prefix) })
}

// TestModelsListReflectsConnectedProviders pins the picker contract a client
// depends on: /v1/models answers the OpenAI list shape, and once connections
// exist it advertises the connected provider's models instead of the whole
// static catalog. A client that picks a model from this list and gets a 502
// because that provider was never connected is the exact failure this catches.
func TestModelsListReflectsConnectedProviders(t *testing.T) {
	env, _ := newProviderEnv(t)

	res := env.Get(t, "/v1/models")
	if res.Status != http.StatusOK {
		t.Fatalf("GET /v1/models = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}

	var list modelsResponse
	res.Decode(t, &list)
	if list.Object != "list" {
		t.Errorf("object = %q, want \"list\"", list.Object)
	}
	if list.Connections != 1 {
		t.Errorf("connections = %d, want 1 (the one seeded connection)", list.Connections)
	}
	if len(list.Data) == 0 {
		t.Fatal("model list is empty, want the connected provider's catalog")
	}

	// Model ids are published under the provider's short alias (ds/ for
	// deepseek), which is the form clients copy into their config.
	if !lo.Contains(list.ids(), "ds/deepseek-chat") {
		t.Errorf("model list = %v, want it to include ds/deepseek-chat", list.ids())
	}
	// An unconnected provider must not be advertised: a client that picks from
	// this list would get a 502 on the call.
	if list.hasPrefix("gq/") {
		t.Errorf("model list = %v, want no groq models while groq has no connection", list.ids())
	}

	for _, entry := range list.Data {
		if entry.ID == "" || entry.Object != "model" {
			t.Errorf("model entry %+v, want a non-empty id and object \"model\"", entry)
		}
	}
}

// TestModelsListIncludesCombos pins that a combo is advertised as an addressable
// model. Without it, clients cannot discover or select the operator's combos.
func TestModelsListIncludesCombos(t *testing.T) {
	env, _ := newProviderEnv(t)
	env.AddCombo(t, "combo-1", "resilient", []string{"deepseek/deepseek-chat"})

	res := env.Get(t, "/v1/models")
	if res.Status != http.StatusOK {
		t.Fatalf("GET /v1/models = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}

	var list modelsResponse
	res.Decode(t, &list)
	if !lo.Contains(list.ids(), "resilient") {
		t.Fatalf("model list = %v, want it to include the combo \"resilient\"", list.ids())
	}
	// Combos come first, matching upstream's ordering.
	if list.Data[0].ID != "resilient" {
		t.Errorf("first model entry = %+v, want the combo first", list.Data[0])
	}
	if list.Data[0].OwnedBy != "combo" {
		t.Errorf("combo owned_by = %q, want \"combo\"", list.Data[0].OwnedBy)
	}
}

// TestModelsInfoDescribesASingleModel pins /v1/models/info, which editor
// plugins call to size a context window before sending a prompt.
func TestModelsInfoDescribesASingleModel(t *testing.T) {
	env, _ := newProviderEnv(t)

	res := env.Get(t, "/v1/models/info?id=deepseek/deepseek-chat")
	if res.Status != http.StatusOK {
		t.Fatalf("GET /v1/models/info = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}

	var info struct {
		ID           string `json:"id"`
		Object       string `json:"object"`
		OwnedBy      string `json:"owned_by"`
		Endpoint     string `json:"endpoint"`
		ContextLen   int    `json:"context_length"`
		MaxOutTokens int    `json:"max_output_tokens"`
	}
	res.Decode(t, &info)
	if info.ID != "deepseek/deepseek-chat" {
		t.Errorf("id = %q, want the requested model id", info.ID)
	}
	if info.OwnedBy != "deepseek" {
		t.Errorf("owned_by = %q, want the owning provider", info.OwnedBy)
	}
	if info.Endpoint != "/v1/chat/completions" {
		t.Errorf("endpoint = %q, want /v1/chat/completions", info.Endpoint)
	}
	if info.ContextLen <= 0 {
		t.Errorf("context_length = %d, want a positive limit", info.ContextLen)
	}
}
