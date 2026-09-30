package chat

import (
	"strconv"
	"strings"
	"testing"
)

func TestIsSelfBaseURL(t *testing.T) {
	const p = 20128
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"loopback ipv4 same port", "http://127.0.0.1:20128/v1", true},
		{"localhost same port", "http://localhost:20128/v1", true},
		{"loopback ipv6 same port", "http://[::1]:20128/v1", true},
		{"wildcard same port", "http://0.0.0.0:20128", true},
		{"empty host same port", "http://:20128/v1", true},
		{"different port", "http://127.0.0.1:45300/v1", false},
		{"public host same port", "https://api.example.com:20128/v1", false},
		{"lan ip same port (legit local node)", "http://192.168.1.7:20128", false},
		{"no port", "http://127.0.0.1", false},
		{"empty", "", false},
		{"garbage", "::::", false},
	}
	for _, tc := range cases {
		if got := isSelfBaseURL(tc.raw, p); got != tc.want {
			t.Errorf("%s: isSelfBaseURL(%q) = %v, want %v", tc.name, tc.raw, got, tc.want)
		}
	}
}

// 守卫必须真的挡在 GetProviderConfig 上（chat/media/responses 全部经它取上游配置），
// 且不得误伤合法的本地上游（连接指向 127.0.0.1 的其他端口是正常用法）。
func TestGetProviderConfig_RejectsSelfLoop(t *testing.T) {
	h := &ChatHandler{}
	self := "http://127.0.0.1:" + strconv.Itoa(ownListenPort()) + "/v1"
	if _, err := h.GetProviderConfig("custom-conn", &ConnectionData{BaseURL: self}); err == nil ||
		!strings.Contains(err.Error(), "self-forward") {
		t.Fatalf("self-loop baseUrl must be rejected, got err=%v", err)
	}
	if _, err := h.GetProviderConfig("custom-conn", &ConnectionData{BaseURL: "http://127.0.0.1:45300/v1"}); err != nil {
		t.Fatalf("legit local upstream must pass, got %v", err)
	}
}
