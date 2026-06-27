package helps

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	tls "github.com/refraction-networking/utls"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"
)

// utlsRoundTripper implements http.RoundTripper using utls with Chrome fingerprint
// to bypass Cloudflare's TLS fingerprinting on Anthropic domains.
type utlsRoundTripper struct {
	mu          sync.Mutex
	connections map[string]*http2.ClientConn
	pending     map[string]*sync.Cond
	dialer      proxy.Dialer
}

func newUtlsRoundTripper(proxyURL string) *utlsRoundTripper {
	var dialer proxy.Dialer = proxy.Direct
	if proxyURL != "" {
		proxyDialer, mode, errBuild := proxyutil.BuildDialer(proxyURL)
		if errBuild != nil {
			log.Errorf("utls: failed to configure proxy dialer for %q: %v", proxyutil.Redact(proxyURL), errBuild)
		} else if mode != proxyutil.ModeInherit && proxyDialer != nil {
			dialer = proxyDialer
		}
	}
	return &utlsRoundTripper{
		connections: make(map[string]*http2.ClientConn),
		pending:     make(map[string]*sync.Cond),
		dialer:      dialer,
	}
}

func (t *utlsRoundTripper) getOrCreateConnection(host, addr string) (*http2.ClientConn, error) {
	t.mu.Lock()

	if h2Conn, ok := t.connections[host]; ok && h2Conn.CanTakeNewRequest() {
		t.mu.Unlock()
		return h2Conn, nil
	}

	if cond, ok := t.pending[host]; ok {
		cond.Wait()
		if h2Conn, ok := t.connections[host]; ok && h2Conn.CanTakeNewRequest() {
			t.mu.Unlock()
			return h2Conn, nil
		}
	}

	cond := sync.NewCond(&t.mu)
	t.pending[host] = cond
	t.mu.Unlock()

	h2Conn, err := t.createConnection(host, addr)

	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.pending, host)
	cond.Broadcast()

	if err != nil {
		return nil, err
	}

	t.connections[host] = h2Conn
	return h2Conn, nil
}

func (t *utlsRoundTripper) createConnection(host, addr string) (*http2.ClientConn, error) {
	conn, err := t.dialer.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{ServerName: host}
	tlsConn := tls.UClient(conn, tlsConfig, tls.HelloChrome_Auto)

	if err := tlsConn.Handshake(); err != nil {
		conn.Close()
		return nil, err
	}

	tr := &http2.Transport{}
	h2Conn, err := tr.NewClientConn(tlsConn)
	if err != nil {
		tlsConn.Close()
		return nil, err
	}

	return h2Conn, nil
}

func (t *utlsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	hostname := req.URL.Hostname()
	port := req.URL.Port()
	if port == "" {
		port = "443"
	}
	addr := net.JoinHostPort(hostname, port)

	resp, h2Conn, err := t.attempt(req, hostname, addr)
	// HTTP/2 single-conn-per-host means a poisoned conn (e.g. ChatGPT-side
	// abuse RST) will fail every subsequent stream on the same TCP+TLS
	// session until evicted. attempt() already evicts on RoundTrip errors;
	// here we retry once if the error looks stream-level and the body is
	// resettable.
	if err != nil && isStreamLevelError(err) && canResetBody(req) {
		if resetErr := resetReqBody(req); resetErr == nil {
			resp, h2Conn, err = t.attempt(req, hostname, addr)
		}
	}
	if err != nil {
		return nil, err
	}
	// Wrap Body so that stream-mid errors (the common ChatGPT RST pattern)
	// also evict the conn — RoundTrip itself returns success in those cases.
	return wrapBodyForEvict(resp, t, hostname, h2Conn), nil
}

// attempt issues one RoundTrip and evicts the cached conn on transport error.
func (t *utlsRoundTripper) attempt(req *http.Request, hostname, addr string) (*http.Response, *http2.ClientConn, error) {
	h2Conn, err := t.getOrCreateConnection(hostname, addr)
	if err != nil {
		return nil, nil, err
	}
	resp, err := h2Conn.RoundTrip(req)
	if err != nil {
		t.evictIfCurrent(hostname, h2Conn)
		return nil, nil, err
	}
	return resp, h2Conn, nil
}

// evictIfCurrent removes the cached conn for host iff it is still the given one.
func (t *utlsRoundTripper) evictIfCurrent(hostname string, conn *http2.ClientConn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if cached, ok := t.connections[hostname]; ok && cached == conn {
		delete(t.connections, hostname)
	}
}

// wrapBodyForEvict replaces resp.Body so that any non-EOF read error triggers
// eviction of the underlying conn. Idempotent: the eviction fires at most once
// per response body, even with many concurrent readers.
func wrapBodyForEvict(resp *http.Response, t *utlsRoundTripper, hostname string, conn *http2.ClientConn) *http.Response {
	if resp == nil || resp.Body == nil {
		return resp
	}
	resp.Body = &evictOnReadErrBody{
		ReadCloser: resp.Body,
		onErr: func() {
			t.evictIfCurrent(hostname, conn)
		},
	}
	return resp
}

// evictOnReadErrBody wraps an HTTP response body. The first non-EOF Read error
// triggers onErr exactly once (via sync.Once).
type evictOnReadErrBody struct {
	io.ReadCloser
	once  sync.Once
	onErr func()
}

func (b *evictOnReadErrBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && err != io.EOF {
		b.once.Do(b.onErr)
	}
	return n, err
}

// isStreamLevelError matches HTTP/2 stream-level errors that suggest the
// underlying conn is poisoned and a fresh dial would likely succeed.
func isStreamLevelError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "PROTOCOL_ERROR") ||
		strings.Contains(s, "INTERNAL_ERROR") ||
		strings.Contains(s, "REFUSED_STREAM") ||
		strings.Contains(s, "stream error") ||
		strings.Contains(s, "stream closed")
}

// canResetBody reports whether the request body can be safely re-read for a retry.
func canResetBody(req *http.Request) bool {
	return req.Body == nil || req.GetBody != nil
}

// resetReqBody closes the current body and replaces it with a fresh copy from
// GetBody. Caller must ensure canResetBody(req).
func resetReqBody(req *http.Request) error {
	if req.Body == nil {
		return nil
	}
	_ = req.Body.Close()
	newBody, err := req.GetBody()
	if err != nil {
		return err
	}
	req.Body = newBody
	return nil
}

// utlsProtectedHosts contains the hosts that should use utls Chrome TLS fingerprint
// to bypass Cloudflare's TLS fingerprinting.
var utlsProtectedHosts = map[string]struct{}{
	"api.anthropic.com": {},
	"chatgpt.com":       {},
}

// fallbackRoundTripper uses utls for protected HTTPS hosts and falls back to
// standard transport for all other requests.
type fallbackRoundTripper struct {
	utls     http.RoundTripper
	fallback http.RoundTripper
}

func (f *fallbackRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "https" {
		if _, ok := utlsProtectedHosts[strings.ToLower(req.URL.Hostname())]; ok {
			return f.utls.RoundTrip(req)
		}
	}
	return f.fallback.RoundTrip(req)
}

// NewUtlsHTTPClient creates an HTTP client using utls Chrome TLS fingerprint.
// Use this for provider requests that need a Chrome-like TLS fingerprint.
// Falls back to standard transport for non-HTTPS requests.
func NewUtlsHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, timeout time.Duration) *http.Client {
	var proxyURL string
	if auth != nil {
		proxyURL = strings.TrimSpace(auth.ProxyURL)
	}
	if proxyURL == "" && cfg != nil {
		proxyURL = strings.TrimSpace(cfg.ProxyURL)
	}

	var ctxRoundTripper http.RoundTripper
	if ctx != nil {
		ctxRoundTripper, _ = ctx.Value("cliproxy.roundtripper").(http.RoundTripper)
	}

	var utlsRT http.RoundTripper = newUtlsRoundTripper(proxyURL)
	var standardTransport http.RoundTripper = http.DefaultTransport
	if proxyURL != "" {
		if transport := buildProxyTransport(proxyURL); transport != nil {
			standardTransport = transport
		}
	} else if ctxRoundTripper != nil {
		utlsRT = ctxRoundTripper
		standardTransport = ctxRoundTripper
	}

	client := &http.Client{
		Transport: &fallbackRoundTripper{
			utls:     utlsRT,
			fallback: standardTransport,
		},
	}
	if timeout > 0 {
		client.Timeout = timeout
	}
	return client
}
