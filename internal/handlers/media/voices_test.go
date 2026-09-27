package media

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"9router/proxy/internal/db"
)

func TestHandleAudioVoices_EdgeTTS(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"ShortName":"en-US-AriaNeural","FriendlyName":"Microsoft Aria Online (Natural) - English (United States)","Locale":"en-US","Gender":"Female"},
			{"ShortName":"en-US-GuyNeural","FriendlyName":"Microsoft Guy Online (Natural) - English (United States)","Locale":"en-US","Gender":"Male"},
			{"ShortName":"fr-FR-DeniseNeural","FriendlyName":"Microsoft Denise Online (Natural) - French (France)","Locale":"fr-FR","Gender":"Female"}
		]`))
	}))
	defer upstream.Close()

	origURL := edgeVoicesURL
	edgeVoicesURL = upstream.URL + "/list"
	defer func() {
		edgeVoicesURL = origURL
		voiceCacheMu.Lock()
		edgeVoiceCache.voices = nil
		voiceCacheMu.Unlock()
	}()
	voiceCacheMu.Lock()
	edgeVoiceCache.voices = nil
	voiceCacheMu.Unlock()
	handler := NewMediaHandler(nil, nil, nil)
	req := httptest.NewRequest("GET", "/audio/voices?lang=en", nil)
	rec := httptest.NewRecorder()
	handler.HandleAudioVoices(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Voices    []map[string]any `json:"voices"`
		Languages []map[string]any `json:"languages"`
		ByLang    map[string]any   `json:"byLang"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}

	if len(resp.Voices) != 2 {
		t.Fatalf("expected 2 voices after lang=en filter, got %d: %v", len(resp.Voices), resp.Voices)
	}
	v := resp.Voices[0]
	if v["name"] != "Aria (English (United States)" {
		t.Errorf("name normalization wrong: %q", v["name"])
	}
	if v["langName"] != "English" || v["countryName"] != "United States" {
		t.Errorf("names wrong: langName=%v countryName=%v", v["langName"], v["countryName"])
	}

	if len(resp.Languages) != 1 || resp.Languages[0]["code"] != "en" {
		t.Errorf("expected single en language group, got %v", resp.Languages)
	}
	en, ok := resp.ByLang["en"].(map[string]any)
	if !ok {
		t.Fatalf("expected en group in byLang, got %v", resp.ByLang)
	}
	if got := len(en["voices"].([]any)); got != 2 {
		t.Errorf("expected 2 voices in en group, got %d", got)
	}
}

func TestHandleAudioVoices_UnsupportedProvider(t *testing.T) {
	handler := NewMediaHandler(nil, nil, nil)
	req := httptest.NewRequest("GET", "/audio/voices?provider=nonexistent", nil)
	rec := httptest.NewRecorder()
	handler.HandleAudioVoices(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func newVoicesTestRepo(t *testing.T) *db.Repo {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "test_voices_*.sqlite")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.Close()
	database, err := db.OpenDatabase(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("OpenDatabase failed: %v", err)
	}
	t.Cleanup(func() {
		database.Close()
		os.Remove(tmpFile.Name())
	})
	if _, err := database.Exec(`CREATE TABLE providerConnections (
		id TEXT PRIMARY KEY,
		provider TEXT NOT NULL,
		authType TEXT NOT NULL,
		name TEXT,
		email TEXT,
		priority INTEGER,
		isActive INTEGER DEFAULT 1,
		data TEXT NOT NULL,
		createdAt TEXT NOT NULL,
		updatedAt TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := db.EnsureAdditiveColumns(database); err != nil {
		t.Fatalf("additive columns: %v", err)
	}
	return db.NewRepo(database)
}

func TestHandleAudioVoices_DeepgramNoConnection(t *testing.T) {
	repo := newVoicesTestRepo(t)
	handler := NewMediaHandler(repo, nil, nil)
	req := httptest.NewRequest("GET", "/audio/voices?provider=deepgram", nil)
	rec := httptest.NewRecorder()
	handler.HandleAudioVoices(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.Error.Message != "No Deepgram connection found" {
		t.Errorf("wrong error: %q", resp.Error.Message)
	}
}

func TestHandleAudioVoices_MinimaxNoConnection(t *testing.T) {
	repo := newVoicesTestRepo(t)
	handler := NewMediaHandler(repo, nil, nil)
	req := httptest.NewRequest("GET", "/audio/voices?provider=minimax", nil)
	rec := httptest.NewRecorder()
	handler.HandleAudioVoices(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
}
