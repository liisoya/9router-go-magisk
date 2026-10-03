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

// 10s 的握手超时会把无线链路上的卡死原样报成 502（真机事故）。默认值必须已经
// 收窄到 3s，同时保留一个环境变量入口，避免为了调一个数字就重编译刷机。
func TestDefaultHTTPTransportConfig_TLSHandshakeTimeoutDefaultsTo3s(t *testing.T) {
	if DefaultHTTPTransportConfig.TLSHandshakeTimeout != 3*time.Second {
		t.Errorf("TLSHandshakeTimeout default = %v, want 3s", DefaultHTTPTransportConfig.TLSHandshakeTimeout)
	}
}

func TestEnvSeconds(t *testing.T) {
	const key = "TEST_ENV_SECONDS"
	def := 3 * time.Second

	cases := []struct {
		name string
		set  bool
		val  string
		want time.Duration
	}{
		{"unset falls back", false, "", def},
		{"empty falls back", true, "", def},
		{"blank falls back", true, "   ", def},
		{"valid override", true, "12", 12 * time.Second},
		{"padded override", true, " 7 ", 7 * time.Second},
		{"garbage falls back", true, "abc", def},
		{"fractional falls back", true, "2.5", def},
		{"zero falls back", true, "0", def},
		{"negative falls back", true, "-5", def},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(key, tc.val)
			}
			if got := envSeconds(key, def); got != tc.want {
				t.Errorf("envSeconds(%q) = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
}
