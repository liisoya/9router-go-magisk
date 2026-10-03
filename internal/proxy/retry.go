package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"

	"9router/proxy/internal/log"
)

// transientRetryPolicy is the Go form of upstream's DEFAULT_RETRY_CONFIG
// (open-sse/config/runtimeConfig.js:78-83): how many extra attempts a
// temporarily-unavailable upstream gets, and how long to wait between them.
//
//	502: 3 attempts @3s   503: 3 attempts @2s   504: 2 attempts @3s
//
// 429 is deliberately absent. Upstream sets its attempts to 0 there so a
// throttled credential is handed to the next account instead of being retried
// against the same window, and this gateway keeps that contract: a 429 belongs
// to the account-fallback path, not here.
var transientRetryPolicy = map[int]transientRetry{
	http.StatusBadGateway:         {attempts: 3, delay: 3 * time.Second},
	http.StatusServiceUnavailable: {attempts: 3, delay: 2 * time.Second},
	http.StatusGatewayTimeout:     {attempts: 2, delay: 3 * time.Second},
}

// transportRetryPolicy 是连接层失败的重拨策略，与 transientRetryPolicy 是两种
// 不同的等待，刻意不共用：上游 502/503 是"对端过载"，要退避；连接层失败是
// "这条连接死了"，重拨即可，退避只会白白拉长用户等待。
//
// attempts=2（外加首次共 3 次）、间隔 400ms：真机实测多数失败是瞬时挂断
// （RST），少数耗满 TLSHandshakeTimeout，所以间隔取亚秒级而非秒级。
var transportRetryPolicy = transientRetry{attempts: 2, delay: 400 * time.Millisecond}

type transientRetry struct {
	attempts int
	delay    time.Duration
}

// IsTransientTransportError 判断 err 是否为"重拨一次就可能好"的连接层失败。
//
// 与状态码重试最关键的区别：这类失败发生在 TLS 握手完成之前，任何应用数据
// 都还没发出去，上游没有收到过这个请求 —— 所以重拨既不会留下半截请求，也不
// 会产生重复计费。这是它可以被自动重试而 502 不敢自动重试的原因。
//
// 排除项各有理由：
//   - 代理失败：ambient 代理 doRequestOnce 已经直连重拨过；operator 指派的
//     代理必须显式失败（见 doRequestOnce 注释），不能被静默重试掩盖。
//   - 客户端已断开 / ctx 超时：没人读重试的结果，不该让请求继续占着连接。
//   - DNS 解析失败：重拨不会让一个解析不出来的名字出现。
func IsTransientTransportError(err error) bool {
	if err == nil {
		return false
	}
	if isProxyFailure(err, nil) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return false
	}
	// *url.Error（含 TLS handshake timeout）实现了 net.Error。
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE)
}

// retryTransientUpstream repeats a failed attempt per the matching policy, so an
// upstream that answers `503 service_overloaded` (opencode-zen's Console backend
// under load) — or a connection that stalls during the TLS handshake — gets
// another chance inside the same client turn instead of surfacing as a failed
// request.
//
// The retry lives at the transport choke point rather than in each provider,
// because upstream applies it in BaseExecutor.execute — the shared path every
// provider inherits. It only ever runs before any response byte has reached the
// client, so a repeat cannot duplicate a partially delivered answer.
//
// Two failure classes are handled, and they are kept apart on purpose:
//   - 状态码（transientRetryPolicy）：对端过载，退避后重发。收到过响应头，
//     所以重发可能让上游重复执行一次。
//   - 连接层（transportRetryPolicy）：握手都没完成，上游没见过这个请求，
//     重拨没有副作用 —— 见 IsTransientTransportError。
//
// A client that has gone away must not be kept waiting out either backoff;
// sleepCtx cuts both short.
func retryTransientUpstream(
	ctx context.Context,
	send func() (*http.Response, error),
) (*http.Response, error) {
	resp, err := send()
	if err == nil {
		return resp, nil
	}
	if IsTransientTransportError(err) {
		return retryTransport(ctx, send, err)
	}

	var ue *UpstreamError
	if !errors.As(err, &ue) {
		return resp, err
	}

	policy, retryable := transientRetryPolicy[ue.StatusCode]
	for attempt := 1; retryable && attempt <= policy.attempts; attempt++ {
		if waitErr := sleepCtx(ctx, policy.delay); waitErr != nil {
			// The client is gone or the deadline passed: report the upstream
			// failure already in hand rather than starting a turn nobody reads.
			log.Debug("retry", "retry abandoned, client gone", "status", ue.StatusCode, "attempt", attempt)
			return nil, err
		}
		log.Debug("retry", "transient upstream failure, retrying", "status", ue.StatusCode, "attempt", attempt, "max", policy.attempts, "delay_ms", policy.delay.Milliseconds())

		resp, err = send()
		if !errors.As(err, &ue) {
			return resp, err
		}
		_, retryable = transientRetryPolicy[ue.StatusCode]
	}
	return resp, err
}

// retryTransport re-dials a connection-level failure per transportRetryPolicy.
// `first` is the error the caller already holds, so a client that disappears
// mid-backoff is reported the original failure rather than a later one.
func retryTransport(
	ctx context.Context,
	send func() (*http.Response, error),
	first error,
) (*http.Response, error) {
	resp, err := send()
	for attempt := 1; IsTransientTransportError(err) && attempt <= transportRetryPolicy.attempts; attempt++ {
		if waitErr := sleepCtx(ctx, transportRetryPolicy.delay); waitErr != nil {
			log.Debug("retry", "re-dial abandoned, client gone", "attempt", attempt)
			return nil, first
		}
		log.Debug("retry", "transport failure, re-dialling", "attempt", attempt, "max", transportRetryPolicy.attempts, "delay_ms", transportRetryPolicy.delay.Milliseconds(), "err", err)
		resp, err = send()
	}
	return resp, err
}

// sleepCtx waits for d, or returns early when ctx ends. A plain time.Sleep would
// keep the request alive for the whole backoff after the client disconnected.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
