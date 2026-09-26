package media

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/db"
)

func TestRewriteMultipartModelPart_ByteExact(t *testing.T) {
	var orig bytes.Buffer
	mw := multipart.NewWriter(&orig)
	_ = mw.WriteField("model", "ag/grok-video")
	fw, _ := mw.CreateFormFile("file", "clip.mp4")
	// Poison bytes: file content contains the model string verbatim.
	poison := []byte("binary\x00ag/grok-video\xffdata")
	_, _ = fw.Write(poison)
	ct := mw.FormDataContentType()
	_ = mw.Close()

	out, err := rewriteMultipartModelPart(orig.Bytes(), ct, "ag/grok-video", "xai/grok-video")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	mr := multipart.NewReader(bytes.NewReader(out), strings.Split(ct, "boundary=")[1])
	got := map[string][]byte{}
	names := map[string]string{}
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(p)
		got[p.FormName()] = b
		names[p.FormName()] = p.FileName()
	}
	if string(got["model"]) != "xai/grok-video" {
		t.Errorf("model not rewritten: %q", got["model"])
	}
	if !bytes.Equal(got["file"], poison) {
		t.Errorf("file bytes corrupted: %q", got["file"])
	}
	if names["file"] != "clip.mp4" {
		t.Errorf("filename lost: %q", names["file"])
	}
}

func TestIsVideoCreateEndpoint(t *testing.T) {
	for _, ep := range []string{"/v1/videos/generations", "/videos/edits", "/v1/videos/extensions"} {
		if !isVideoCreateEndpoint(ep) {
			t.Errorf("expected video create: %s", ep)
		}
	}
	if isVideoCreateEndpoint("/v1/search") || isVideoCreateEndpoint("/v1/images/generations") {
		t.Errorf("non-video endpoint misclassified")
	}
}

func TestVideoGet_InvalidID(t *testing.T) {
	database, cleanup := setupMultimodalTestDB(t)
	defer cleanup()
	handler := newTestMediaHandler(db.NewRepo(database))
	for _, id := range []string{"..", "a/b", "a\\b"} {
		req := httptest.NewRequest(http.MethodGet, "/videos/"+id, nil)
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		handler.HandleVideoGet(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("id %q: expected 400, got %d", id, rec.Code)
		}
	}
}
