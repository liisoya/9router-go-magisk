package chat

import (
	"net/url"
	"strconv"
	"strings"
	"sync"

	"9router/proxy/internal/config"
)

// Self-forwarding guard: 把 provider connection 的 baseUrl 配成本代理自己的监听地址时，
// 请求链路会重新进入自己的 chat handler 按连接策略继续转发 —— 每跳都真实消耗一次轮转记账、
// usage 落库、goroutine/SSE 资源，直到客户端超时或连接耗尽（2026-09-30 真机观测：用户曾把
// 引擎端口与某连接的 baseUrl 同时放在 45300）。守卫只做**窄比较**：
//
//	回环主机名 + 端口与本机监听完全一致 → 判自环；
//	显式非回环主机（哪怕是局域网 IP、同端口）→ 不判自环。
//
// 不能复用 handlerutil.AssertPublicURL：它会封杀一切私网/回环，把合法的本机 Ollama 与
// 局域网自建节点一并误杀（dashboard/provider_nodes.go 对 loopback 调用方豁免正是同一理由）。
var (
	ownPortOnce sync.Once
	ownPortVal  int
)

func ownListenPort() int {
	ownPortOnce.Do(func() {
		if cfg := config.LoadConfig(); cfg != nil {
			ownPortVal = cfg.Port
		}
	})
	return ownPortVal
}

func isSelfBaseURL(rawURL string, port int) bool {
	if rawURL == "" || port <= 0 {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Port() != strconv.Itoa(port) {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0", "::", "":
		return true
	}
	return false
}

func isSelfLoopBaseURL(rawURL string) bool { return isSelfBaseURL(rawURL, ownListenPort()) }
