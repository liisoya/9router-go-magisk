package oauth

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"strconv"
	"sync"
	"syscall"
	"time"

	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/log"
)

// Codex's public OAuth client (app_EMoamEEZ73f0CkXaXp7hrann, the Codex CLI) has
// exactly one registered loopback redirect URI. auth.openai.com validates
// redirect_uri during the authorize step, so a dashboard-derived
// http://localhost:<dashboardPort>/callback — or even the right port with the
// wrong path — is rejected with invalid_authorize_request before the login page
// ever renders. Verified against auth.openai.com; only codexRedirectURI works.
//
// That URI has to point at a real listener, so the login flow starts a
// short-lived loopback server on the fixed port, lets the provider redirect
// into it, and completes the code exchange server-side. Mirrors upstream
// startCodexProxy (src/lib/oauth/utils/server.js).
const (
	codexProxyHost        = "127.0.0.1"
	codexProxyPort        = 1455
	codexRedirectURI      = "http://localhost:1455/auth/callback"
	codexProxyIdleTimeout = 5 * time.Minute
	codexExchangeTimeout  = 30 * time.Second
	codexShutdownTimeout  = 5 * time.Second
)

// codexSession is one in-flight login, keyed by the OAuth state the authorize
// step minted. It carries the PKCE verifier across the round trip so the
// loopback server can finish the exchange without the browser.
type codexSession struct {
	codeVerifier string
	redirectURI  string
	name         string
	status       string // pending | done | error
	connectionID string
	email        string
	errMsg       string
	createdAt    time.Time
}

// codexProxy is the process-wide loopback listener for Codex logins. One
// listener serves one login at a time: the port is fixed by the redirect URI
// registered with OpenAI, so a second concurrent Codex login cannot bind
// anyway. port is a field rather than a constant so tests can bind an
// ephemeral one and still exercise the real listener.
type codexProxy struct {
	mu       sync.Mutex
	port     int
	addr     string
	listener net.Listener
	srv      *http.Server
	appPort  string
	sessions map[string]*codexSession
	idle     *time.Timer
	// exchange completes a login; wired by the start handler because it needs
	// the OAuthHandler's repository to persist the connection.
	exchange func(ctx context.Context, code string, sess *codexSession) (pkceExchangeResult, error)
}

var codexLoopback = &codexProxy{port: codexProxyPort, sessions: map[string]*codexSession{}}

// register records a pending login so the loopback listener can complete it.
func (p *codexProxy) register(state, codeVerifier, redirectURI, name string) {
	if state == "" || codeVerifier == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sessions[state] = &codexSession{
		codeVerifier: codeVerifier,
		redirectURI:  redirectURI,
		name:         name,
		status:       "pending",
		createdAt:    time.Now(),
	}
}

// lookup returns a copy of the session so callers never observe a torn read
// while the loopback handler is writing the result fields.
func (p *codexProxy) lookup(state string) (codexSession, bool) {
	if state == "" {
		return codexSession{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	sess, ok := p.sessions[state]
	if !ok {
		return codexSession{}, false
	}
	return *sess, true
}

func (p *codexProxy) complete(state, connectionID, email string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sess, ok := p.sessions[state]; ok {
		sess.status = "done"
		sess.connectionID = connectionID
		sess.email = email
	}
}

func (p *codexProxy) fail(state string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sess, ok := p.sessions[state]; ok {
		sess.status = "error"
		sess.errMsg = err.Error()
	}
}

func (p *codexProxy) clear(state string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.sessions, state)
}

// start binds the fixed loopback port and arms the idle timer. A port that is
// already taken is reported as such rather than retried elsewhere: the
// redirect URI registered with OpenAI names port 1455, so listening anywhere
// else would be a silent dead end for the browser.
func (p *codexProxy) start(appPort string, exchangeFn func(context.Context, string, *codexSession) (pkceExchangeResult, error)) error {
	p.mu.Lock()
	if p.listener != nil {
		p.mu.Unlock()
		return nil // already running; the session registry is what matters
	}
	p.mu.Unlock()

	// port 0 asks the OS for an ephemeral port; the process-wide proxy pins
	// codexProxyPort because that is the only one OpenAI redirects to.
	port := p.port
	ln, err := net.Listen("tcp", net.JoinHostPort(codexProxyHost, strconv.Itoa(port)))
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.listener = ln
	p.addr = ln.Addr().String()
	p.appPort = appPort
	p.exchange = exchangeFn
	srv := &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 10 * time.Second,
	}
	p.srv = srv
	if p.idle != nil {
		p.idle.Stop()
	}
	p.idle = time.AfterFunc(codexProxyIdleTimeout, p.stop)
	p.mu.Unlock()

	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			log.Warn("oauth", "codex loopback proxy stopped", "err", serveErr.Error())
		}
	}()
	return nil
}

// stop tears the listener down. A login that is still in flight is left in the
// session registry: the dashboard may still poll for its result, and the
// server-side exchange is not tied to the listener's lifetime.
//
// Shutdown, not Close: Close also kills connections that are mid-response,
// which from inside a request handler truncates the very result page the user
// is looking at. Shutdown stops accepting and lets in-flight requests finish.
func (p *codexProxy) stop() {
	p.mu.Lock()
	srv, idle := p.srv, p.idle
	p.srv, p.listener, p.idle = nil, nil, nil
	p.mu.Unlock()

	if idle != nil {
		idle.Stop()
	}
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), codexShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			_ = srv.Close()
		}
	}
}

// stopAsync retires the listener from inside a request handler. Shutdown waits
// for active requests to complete, so calling it on the handler's own goroutine
// would wait on the response it is about to write.
func (p *codexProxy) stopAsync() {
	go p.stop()
}

// stopIfIdle retires the listener only once no login is still waiting on it.
// A stray callback — a leftover popup, a second browser, a real codex CLI on
// the same machine — must not tear down someone else's in-flight login; the
// port is fixed, so a dead listener means a dead login with no way back.
func (p *codexProxy) stopIfIdle() {
	p.mu.Lock()
	pending := false
	for _, sess := range p.sessions {
		if sess.status == "pending" {
			pending = true
			break
		}
	}
	p.mu.Unlock()
	if !pending {
		p.stopAsync()
	}
}

// ServeHTTP handles the loopback redirect. It is deliberately not mounted on
// the main router: OpenAI redirects the browser here by port, so it has to be
// reachable without the dashboard session.
func (p *codexProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/auth/callback" && r.URL.Path != "/callback" {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	state := q.Get("state")
	sess, registered := p.lookup(state)
	if !registered {
		// No session: the browser arrived from a login started elsewhere (a
		// real codex CLI, or a dashboard tab that already closed its session).
		// Bounce the code to the dashboard's /callback page, which already
		// knows how to hand it back to an open modal.
		p.redirectToDashboard(w, r)
		p.stopIfIdle()
		return
	}
	p.handleSessionCallback(w, r, state, sess)
}

func (p *codexProxy) handleSessionCallback(w http.ResponseWriter, r *http.Request, state string, sess codexSession) {
	q := r.URL.Query()
	if errParam := q.Get("error"); errParam != "" {
		detail := q.Get("error_description")
		if detail == "" {
			detail = errParam
		}
		err := fmt.Errorf("authorization denied: %s", detail)
		p.fail(state, err)
		p.writeResultPage(w, false, err.Error())
		p.stopIfIdle()
		return
	}
	code := q.Get("code")
	if code == "" {
		err := fmt.Errorf("no authorization code received")
		p.fail(state, err)
		p.writeResultPage(w, false, err.Error())
		p.stopIfIdle()
		return
	}

	// Detach from the request context: closing the popup must not abort a token
	// exchange that is already in flight, or the account would be left
	// half-authorized with a burned code.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), codexExchangeTimeout)
	defer cancel()

	res, err := p.runExchange(ctx, code, &sess)
	if err != nil {
		p.fail(state, err)
		p.writeResultPage(w, false, err.Error())
	} else {
		p.complete(state, res.ConnectionID, res.Email)
		p.writeResultPage(w, true, fmt.Sprintf("Connected as %s. You can close this window.", res.Name))
	}
	p.stopIfIdle()
}

func (p *codexProxy) runExchange(ctx context.Context, code string, sess *codexSession) (pkceExchangeResult, error) {
	p.mu.Lock()
	exchange := p.exchange
	p.mu.Unlock()
	if exchange == nil {
		return pkceExchangeResult{}, fmt.Errorf("loopback proxy has no exchange handler")
	}
	return exchange(ctx, code, sess)
}

func (p *codexProxy) redirectToDashboard(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	appPort := p.appPort
	p.mu.Unlock()
	if appPort == "" {
		http.Error(w, "codex login was not started by this dashboard", http.StatusBadRequest)
		return
	}
	target := "http://localhost:" + appPort + "/callback"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	w.Header().Set("Location", target)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusFound)
}

// writeResultPage renders the outcome in the popup the user is looking at.
func (p *codexProxy) writeResultPage(w http.ResponseWriter, ok bool, message string) {
	title, icon, color := "Login failed", "&#10007;", "#b3261e"
	if ok {
		title, icon, color = "You're connected", "&#10003;", "#1a7f37"
	}
	// message carries provider error text, so it is escaped, never interpolated raw.
	page := `<!DOCTYPE html><html><head><meta charset="utf-8"><title>` + title + `</title>
<style>body{font-family:system-ui,-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;background:#f5f5f5}
.c{text-align:center;padding:2rem;background:#fff;border-radius:8px;box-shadow:0 2px 10px rgba(0,0,0,.1);max-width:32rem}
.i{color:` + color + `;font-size:3rem}h1{margin:1rem 0;font-size:1.25rem}p{color:#666;word-break:break-word}</style>
</head><body><div class="c"><div class="i">` + icon + `</div><h1>` + title + `</h1><p>` + html.EscapeString(message) + `</p></div></body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	_, _ = w.Write([]byte(page))
}

// HandleCodexStartProxy starts the fixed-port loopback listener and registers
// the pending login.
// GET /api/oauth/codex/start-proxy?app_port=&state=&code_verifier=&redirect_uri=
func (h *OAuthHandler) HandleCodexStartProxy(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	appPort := q.Get("app_port")
	if appPort == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing app_port")
		return
	}
	state := q.Get("state")
	if state == "" || q.Get("code_verifier") == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing state or code_verifier")
		return
	}
	redirectURI := q.Get("redirect_uri")
	if redirectURI != codexRedirectURI {
		// The registered redirect URI is the only value OpenAI will accept;
		// silently honoring something else would produce invalid_authorize_request.
		handlerutil.WriteJSONError(w, http.StatusBadRequest,
			"codex redirect_uri must be "+codexRedirectURI)
		return
	}

	err := codexLoopback.start(appPort, func(ctx context.Context, code string, sess *codexSession) (pkceExchangeResult, error) {
		ex := &pkceExchange{
			cfg:          pkceProviders["codex"],
			provider:     "codex",
			code:         code,
			codeVerifier: sess.codeVerifier,
			state:        state,
			name:         sess.name,
			redirectURI:  sess.redirectURI,
			clientID:     pkceProviders["codex"].clientID,
			tokenURL:     pkceProviders["codex"].tokenURL,
		}
		res, fail := h.completePKCEExchange(ctx, ex)
		if fail != nil {
			return pkceExchangeResult{}, fmt.Errorf("codex exchange: %s", fail.message)
		}
		return res, nil
	})
	if err != nil {
		reason := "start_failed"
		if isAddrInUse(err) {
			reason = "port_busy"
		}
		handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "reason": reason})
		return
	}
	codexLoopback.register(state, q.Get("code_verifier"), redirectURI, q.Get("name"))
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "serverSide": true, "port": codexProxyPort})
}

// HandleCodexPollStatus reports how the pending login is progressing. A
// finished or failed session is dropped on read, so a reload never resurrects
// a stale result.
// GET /api/oauth/codex/poll-status?state=
func (h *OAuthHandler) HandleCodexPollStatus(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing state")
		return
	}
	sess, ok := codexLoopback.lookup(state)
	if !ok {
		handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"status": "unknown"})
		return
	}
	if sess.status != "done" && sess.status != "error" {
		handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"status": sess.status})
		return
	}
	codexLoopback.clear(state)
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":       sess.status,
		"connectionId": sess.connectionID,
		"email":        sess.email,
		"error":        sess.errMsg,
	})
}

// HandleCodexStopProxy tears the loopback listener down when the user closes
// the OAuth modal before completing the login.
// GET /api/oauth/codex/stop-proxy
func (h *OAuthHandler) HandleCodexStopProxy(w http.ResponseWriter, r *http.Request) {
	codexLoopback.stop()
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// isAddrInUse reports whether binding failed because the port is taken, which
// is the one failure the user can actually act on.
func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
