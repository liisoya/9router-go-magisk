//go:build !windows

package updater

import "9router/proxy/internal/shutdown"

// signalSelfShutdown asks this process to stop through shutdown.RequestStop,
// which main selects on next to SIGINT/SIGTERM. Going through the package
// instead of raising SIGTERM on ourselves keeps one shutdown path: Windows has
// no SIGTERM, so a signal-only route cannot drain the listener there.
func signalSelfShutdown() bool {
	shutdown.RequestStop()
	return true
}
