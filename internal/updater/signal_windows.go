//go:build windows

package updater

import "9router/proxy/internal/shutdown"

// signalSelfShutdown asks this process to stop through shutdown.RequestStop.
// Windows cannot raise SIGTERM on itself (os.Process.Signal returns "not
// supported by windows"), so the request channel main selects on is the only
// way to let the listener drain before the new process binds the same port.
func signalSelfShutdown() bool {
	shutdown.RequestStop()
	return true
}
