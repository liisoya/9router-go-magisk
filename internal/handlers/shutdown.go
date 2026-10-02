package handlers

import (
	"net/http"
	"time"

	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/shutdown"
)

// HandleShutdown handles POST /api/version/shutdown: acknowledges the request
// and then asks this process to stop, so the operator can release file locks or
// restart the gateway from the dashboard.
//
// The reply is written first and the stop is requested a moment later,
// mirroring the Next dashboard's deferred process.exit — otherwise the
// connection would reset before the UI sees the response.
//
// The request goes through shutdown.RequestStop, which main selects on next to
// SIGINT/SIGTERM. Self-signalling SIGTERM cannot work on Windows:
// os.Process.Signal(syscall.SIGTERM) returns "not supported by windows", so
// the dashboard button used to leave the server running.
func HandleShutdown(w http.ResponseWriter, r *http.Request) {
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Shutting down...",
	})

	go func() {
		time.Sleep(500 * time.Millisecond)
		// Cancel first: streams end, then fxApp.Stop drains the listener.
		shutdown.RequestStop()
	}()
}
