package executor

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"9router/proxy/internal/providers"
)

func TestForwardOpencodeGo_DeepSeekStripsResponseFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)

		model, _ := parsed["model"].(string)
		rf := parsed["response_format"]

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		// Check expectations based on model prefix
		if model == "deepseek-chat" || model == "deepseek-flash" || model == "deepseek-v4-pro" || model == "deepseek-v4.1-flash" {
			if rf != nil {
				w.Write([]byte(`{"error":{"message":"response_format should be stripped"}}`))
				return
			}
		} else {
			if rf == nil {
				w.Write([]byte(`{"error":{"message":"response_format should be kept"}}`))
				return
			}
		}

		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	cfg := &providers.ProviderConfig{BaseURL: srv.URL}

	dropModels := []string{"deepseek-chat", "deepseek-flash", "deepseek-v4-pro", "deepseek-v4.1-flash"}
	formats := []string{`{"type":"json_object"}`, `{"type":"json_schema","json_schema":{"name":"test"}}`}

	for _, model := range dropModels {
		for _, fmt := range formats {
			t.Run("drop for "+model+" with "+fmt, func(t *testing.T) {
				rec := httptest.NewRecorder()
				req := &Request{
					Client: srv.Client(),
					Config: cfg,
					APIKey: "key",
					Body:   []byte(`{"model":"` + model + `","response_format":` + fmt + `,"messages":[{"role":"user","content":"hi"}]}`),
				}
				err := ForwardOpencodeGo(rec, req)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if rec.Code != http.StatusOK {
					t.Fatalf("expected status 200, got %d, body: %s", rec.Code, rec.Body.String())
				}
			})
		}
	}

	keepModels := []string{"glm-5.3-flash", "kimi-k3", "mimo-v2.6-flash"}
	for _, model := range keepModels {
		for _, fmt := range formats {
			t.Run("keep for "+model+" with "+fmt, func(t *testing.T) {
				rec := httptest.NewRecorder()
				req := &Request{
					Client: srv.Client(),
					Config: cfg,
					APIKey: "key",
					Body:   []byte(`{"model":"` + model + `","response_format":` + fmt + `,"messages":[{"role":"user","content":"hi"}]}`),
				}
				err := ForwardOpencodeGo(rec, req)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if rec.Code != http.StatusOK {
					t.Fatalf("expected status 200, got %d, body: %s", rec.Code, rec.Body.String())
				}
			})
		}
	}
}
