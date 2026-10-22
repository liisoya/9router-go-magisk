package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"9router/proxy/internal/providers"
)

func TestOpencodeStealthClient_BunProfileSpec(t *testing.T) {
	spec, err := bunBoringSSLSpec()
	if err != nil {
		t.Fatalf("bunBoringSSLSpec() error = %v", err)
	}

	// Baseline requires exactly 17 ciphers
	if len(spec.CipherSuites) != 17 {
		t.Errorf("got %d ciphers, want 17", len(spec.CipherSuites))
	}

	// Baseline requires 13 extensions (12 baseline extensions + SNIExtension)
	if len(spec.Extensions) < 12 {
		t.Errorf("got %d extensions, want at least 12", len(spec.Extensions))
	}

	// Verify client profile builds cleanly
	profile := newOpencodeClientProfile()
	if profile.GetClientHelloStr() == "" {
		t.Error("expected non-empty client hello string")
	}

	client, err := newOpencodeStealthClient(nil)
	if err != nil {
		t.Fatalf("newOpencodeStealthClient() error = %v", err)
	}
	if client == nil || client.client == nil {
		t.Fatal("stealth client or underlying client is nil")
	}
}

func TestOpencodeStealthClient_ProxyPropagation(t *testing.T) {
	// 1. Nil client returns empty proxy
	if got := opencodeProxyFrom(nil); got != "" {
		t.Errorf("expected empty proxy for nil client, got %q", got)
	}

	// 2. Client without transport proxy returns empty
	plainClient := &http.Client{}
	if got := opencodeProxyFrom(plainClient); got != "" {
		t.Errorf("expected empty proxy for unproxied client, got %q", got)
	}

	// 3. Client with configured proxy returns proxy URL
	proxyTarget, _ := url.Parse("http://127.0.0.1:8888")
	proxiedClient := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyTarget),
		},
	}
	if got := opencodeProxyFrom(proxiedClient); got != "http://127.0.0.1:8888" {
		t.Errorf("expected http://127.0.0.1:8888, got %q", got)
	}
}

func TestOpencodeStealthClient_TraceHeaders(t *testing.T) {
	tp, b3 := generateOpenCodeTraceHeaders()

	// traceparent format: 00-{32 hex}-{16 hex}-01
	tpRe := regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`)
	if !tpRe.MatchString(tp) {
		t.Errorf("traceparent %q does not match W3C traceparent format", tp)
	}

	// b3 format: {32 hex}-{16 hex}-1
	b3Re := regexp.MustCompile(`^[0-9a-f]{32}-[0-9a-f]{16}-1$`)
	if !b3Re.MatchString(b3) {
		t.Errorf("b3 %q does not match b3 format", b3)
	}
}

func TestOpencodePlainHTTP_EnvOverride(t *testing.T) {
	oldEnv := os.Getenv("OPENCODE_PLAIN_HTTP")
	defer func() { _ = os.Setenv("OPENCODE_PLAIN_HTTP", oldEnv) }()

	_ = os.Setenv("OPENCODE_PLAIN_HTTP", "")
	if opencodePlainHTTP() {
		t.Error("expected opencodePlainHTTP() to be false when env is unset")
	}

	_ = os.Setenv("OPENCODE_PLAIN_HTTP", "1")
	if !opencodePlainHTTP() {
		t.Error("expected opencodePlainHTTP() to be true when OPENCODE_PLAIN_HTTP=1")
	}
}

func TestForwardOpencodeRequest_PlainHTTPForMockServer(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer public" {
			t.Errorf("expected Authorization Bearer public, got %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello from mock"}}]}`))
	}))
	defer mockServer.Close()

	cfg := &providers.ProviderConfig{
		BaseURL: mockServer.URL,
		NoAuth:  true,
		StaticHeaders: map[string]string{
			"User-Agent":    "opencode/test",
			"Authorization": "Bearer public",
		},
	}

	resp, err := forwardOpencodeRequest(context.Background(), mockServer.Client(), cfg, "public", []byte(`{}`), false)
	if err != nil {
		t.Fatalf("forwardOpencodeRequest() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestForwardOpencode_EndToEndStream(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"test chunk\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer mockServer.Close()

	rec := httptest.NewRecorder()
	req := &Request{
		Ctx: context.Background(),
		Config: &providers.ProviderConfig{
			BaseURL: mockServer.URL,
			NoAuth:  true,
		},
		Client:   mockServer.Client(),
		APIKey:   "public",
		Body:     []byte(`{"model":"oc/mimo-v2.6-flash-free","messages":[{"role":"user","content":"hi"}],"stream":true}`),
		IsStream: true,
	}

	err := ForwardOpencode(rec, req)
	if err != nil {
		t.Fatalf("ForwardOpencode() error = %v", err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "test chunk") {
		t.Errorf("expected response to contain 'test chunk', got %q", body)
	}
}
