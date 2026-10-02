package daemon

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"9router/proxy/internal/proc"
)

// Flag names and the environment marker shared by the CLI, the child argument
// filter, and the tests.
const (
	// BackgroundName is the CLI flag name (urfave/cli rejects a leading dash).
	BackgroundName = "background"
	// BackgroundFlag is the long form as it appears in argv.
	BackgroundFlag = "--" + BackgroundName
	// BackgroundAlias is the short form as it appears in argv.
	BackgroundAlias = "-d"
	// EnvBackground marks a process started as a background daemon.
	EnvBackground = "9ROUTER_BACKGROUND"
)

// HealthPath is the endpoint Start polls; it is public and answers without a
// session or API key.
const HealthPath = "/health"

// IsBackgroundProcess reports whether this process was spawned as a daemon.
func IsBackgroundProcess() bool {
	return os.Getenv(EnvBackground) == "1"
}

// healthClient skips the shared transport pool: this one request is made once,
// and a reused keep-alive would outlive the process that dialed it.
var healthClient = &http.Client{Timeout: 2 * time.Second}

// waitHealthy polls the daemon's /health until it answers, the process dies,
// or StartupTimeout elapses. The process check is what makes a failed bind
// (port already in use) report as an error instead of a hung terminal.
func waitHealthy(url string, pid int) error {
	deadline := time.Now().Add(StartupTimeout)
	target := strings.TrimSuffix(url, "/") + HealthPath

	for {
		if !proc.Alive(pid) {
			return fmt.Errorf("daemon.Start: process %d exited before becoming ready — see %s", pid, LogPath())
		}
		if probeHealthy(target) {
			return nil
		}
		if bindFailureSeen() {
			return fmt.Errorf("daemon.Start: the daemon could not bind its port — see %s", LogPath())
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("daemon.Start: no response from %s within %s — see %s", target, StartupTimeout, LogPath())
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// bindFailureSeen reports whether the daemon logged an address-in-use failure.
// The server treats a failed bind as fatal, but the log line lands a moment
// before the liveness check notices the exit; catching it keeps a failed start
// from burning the whole startup timeout.
func bindFailureSeen() bool {
	data, err := os.ReadFile(LogPath())
	if err != nil {
		return false
	}
	return bytes.Contains(data, []byte("bind: Only one usage of each socket address")) ||
		bytes.Contains(data, []byte("address already in use"))
}

// probeHealthy reports whether /health answers 200.
func probeHealthy(url string) bool {
	resp, err := healthClient.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
