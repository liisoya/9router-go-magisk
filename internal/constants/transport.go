package constants

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// HTTPTransportConfig encapsulates connection pooling and timeout configurations
// for upstream HTTP transports in reverse proxying and streaming workloads.
type HTTPTransportConfig struct {
	// MaxIdleConns controls the maximum number of idle (keep-alive) connections across all hosts.
	MaxIdleConns int

	// MaxIdleConnsPerHost controls the maximum idle (keep-alive) connections to keep per-host.
	// Go standard library defaults this to 2, which causes connection thrashing under high concurrency.
	MaxIdleConnsPerHost int

	// IdleConnTimeout is the maximum amount of time an idle (keep-alive) connection will remain idle before closing itself.
	IdleConnTimeout time.Duration

	// TLSHandshakeTimeout specifies the maximum amount of time waiting to wait for a TLS handshake.
	TLSHandshakeTimeout time.Duration

	// ExpectContinueTimeout specifies the amount of time to wait for a server's first response headers
	// after fully writing the request headers if the request has an "Expect: 100-continue" header.
	ExpectContinueTimeout time.Duration

	// ResponseHeaderTimeout specifies the amount of time to wait for a server's response headers after fully writing the request.
	ResponseHeaderTimeout time.Duration
}

// DefaultHTTPTransportConfig defines the high-throughput production defaults for upstream LLM reverse-proxy connections.
//
// TLSHandshakeTimeout 由 10s 下调到 3s（2026-10-03）：真机实测（CMCC 宽带 →
// EdgeOne）到国际站的 TLS 握手存在间歇性 stall，成功时 0.4–2.3s，卡住时耗满
// 整个超时。10s 只会把每一次卡死原样报成 502，而重拨（新的 5-tuple 可能被
// ECMP 送到另一条路径）实测比干等有效得多 —— 3s×3 的成功率与 5s×3 持平，
// 最坏耗时却从 16s 降到 9.8s。配套重拨在 internal/proxy/retry.go。
// 需要按上游质量调整时用 HTTP_TLS_HANDSHAKE_TIMEOUT（秒）覆盖，不必重编译。
var DefaultHTTPTransportConfig = HTTPTransportConfig{
	MaxIdleConns:          256,
	MaxIdleConnsPerHost:   128,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   envSeconds("HTTP_TLS_HANDSHAKE_TIMEOUT", 3*time.Second),
	ExpectContinueTimeout: 1 * time.Second,
	ResponseHeaderTimeout: 2 * time.Minute,
}

// envSeconds 以整秒为单位读取一个时长环境变量，未设置 / 不可解析 / 非正数
// 时退回 def。配置写错时静默回落到默认值，而不是让网关带着 0 超时启动。
func envSeconds(key string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	return time.Duration(n) * time.Second
}

// Configure applies the connection pool and timeout settings to an existing *http.Transport.
func (c HTTPTransportConfig) Configure(t *http.Transport) {
	if t == nil {
		return
	}
	t.ForceAttemptHTTP2 = true
	t.MaxIdleConns = c.MaxIdleConns
	t.MaxIdleConnsPerHost = c.MaxIdleConnsPerHost
	t.IdleConnTimeout = c.IdleConnTimeout
	t.TLSHandshakeTimeout = c.TLSHandshakeTimeout
	t.ExpectContinueTimeout = c.ExpectContinueTimeout
	t.ResponseHeaderTimeout = c.ResponseHeaderTimeout
}

// NewTransport instantiates a fresh *http.Transport with these settings applied and Proxy: nil.
func (c HTTPTransportConfig) NewTransport() *http.Transport {
	t := &http.Transport{
		Proxy: nil,
	}
	c.Configure(t)
	return t
}
