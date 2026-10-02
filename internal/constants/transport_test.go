package constants

import (
	"net/http"
	"testing"
	"time"
)

func TestHTTPTransportConfig_NewTransport(t *testing.T) {
	cfg := HTTPTransportConfig{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   50,
		IdleConnTimeout:       45 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 2 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}

	tr := cfg.NewTransport()
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}

	if tr.MaxIdleConns != 100 {
		t.Errorf("expected MaxIdleConns=100, got %d", tr.MaxIdleConns)
	}
	if tr.MaxIdleConnsPerHost != 50 {
		t.Errorf("expected MaxIdleConnsPerHost=50, got %d", tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout != 45*time.Second {
		t.Errorf("expected IdleConnTimeout=45s, got %v", tr.IdleConnTimeout)
	}
	if tr.TLSHandshakeTimeout != 5*time.Second {
		t.Errorf("expected TLSHandshakeTimeout=5s, got %v", tr.TLSHandshakeTimeout)
	}
	if tr.ExpectContinueTimeout != 2*time.Second {
		t.Errorf("expected ExpectContinueTimeout=2s, got %v", tr.ExpectContinueTimeout)
	}
	if tr.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("expected ResponseHeaderTimeout=30s, got %v", tr.ResponseHeaderTimeout)
	}
}

func TestHTTPTransportConfig_Configure(t *testing.T) {
	tr := &http.Transport{}
	DefaultHTTPTransportConfig.Configure(tr)
	if tr.MaxIdleConns != DefaultHTTPTransportConfig.MaxIdleConns {
		t.Errorf("expected MaxIdleConns=%d, got %d", DefaultHTTPTransportConfig.MaxIdleConns, tr.MaxIdleConns)
	}
	if tr.MaxIdleConnsPerHost != DefaultHTTPTransportConfig.MaxIdleConnsPerHost {
		t.Errorf("expected MaxIdleConnsPerHost=%d, got %d", DefaultHTTPTransportConfig.MaxIdleConnsPerHost, tr.MaxIdleConnsPerHost)
	}
}

func TestHTTPTransportConfig_Configure_Nil(t *testing.T) {
	// Should not panic on nil transport
	DefaultHTTPTransportConfig.Configure(nil)
}
