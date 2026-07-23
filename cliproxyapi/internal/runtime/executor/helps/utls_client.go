package helps

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tls "github.com/refraction-networking/utls"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"
)

const defaultUtlsPoolSize = 1

type h2ClientConn interface {
	RoundTrip(*http.Request) (*http.Response, error)
	ReserveNewRequest() bool
	Shutdown(context.Context) error
	Close() error
}

type connPool struct {
	conns    []h2ClientConn
	idx      atomic.Uint32
	draining atomic.Int64
	drainWG  sync.WaitGroup
}

type cachedUtlsRoundTrippers struct {
	utls     http.RoundTripper
	fallback http.RoundTripper
}

type utlsRoundTripperCacheKey struct {
	proxyURL string
	poolSize int
}

const maxCachedUtlsRoundTrippers = 128

var utlsRoundTripperCache = struct {
	mu    sync.RWMutex
	items map[utlsRoundTripperCacheKey]cachedUtlsRoundTrippers
}{
	items: make(map[utlsRoundTripperCacheKey]cachedUtlsRoundTrippers),
}

// utlsRoundTripper implements http.RoundTripper using utls with Chrome fingerprint
// to bypass Cloudflare's TLS fingerprinting on Anthropic domains.
type utlsRoundTripper struct {
	mu         sync.Mutex
	pools      map[string]*connPool
	pending    map[string]*sync.Cond
	filling    map[string]bool
	dialer     proxy.Dialer
	poolSize   int
	createConn func(host, addr string) (h2ClientConn, error)
}

func newUtlsRoundTripper(proxyURL string, poolSize int) *utlsRoundTripper {
	var dialer proxy.Dialer = proxy.Direct
	if proxyURL != "" {
		proxyDialer, mode, errBuild := proxyutil.BuildDialer(proxyURL)
		if errBuild != nil {
			log.Errorf("utls: failed to configure proxy dialer for %q: %v", proxyutil.Redact(proxyURL), errBuild)
		} else if mode != proxyutil.ModeInherit && proxyDialer != nil {
			dialer = proxyDialer
		}
	}
	rt := &utlsRoundTripper{
		pools:    make(map[string]*connPool),
		pending:  make(map[string]*sync.Cond),
		filling:  make(map[string]bool),
		dialer:   dialer,
		poolSize: normalizeUtlsPoolSize(poolSize),
	}
	rt.createConn = rt.createConnection
	return rt
}

func normalizeUtlsPoolSize(poolSize int) int {
	if poolSize <= 0 {
		return defaultUtlsPoolSize
	}
	return poolSize
}

func effectiveUtlsPoolSize(cfg *config.Config) int {
	if cfg == nil {
		return defaultUtlsPoolSize
	}
	return normalizeUtlsPoolSize(cfg.UtlsPoolSize)
}

func (t *utlsRoundTripper) poolForHostLocked(host string) *connPool {
	pool := t.pools[host]
	if pool == nil {
		pool = &connPool{}
		t.pools[host] = pool
	}
	return pool
}

func (p *connPool) reserveRoundRobin() h2ClientConn {
	n := len(p.conns)
	if n == 0 {
		return nil
	}
	start := int(p.idx.Add(1)-1) % n
	for offset := 0; offset < n; offset++ {
		conn := p.conns[(start+offset)%n]
		if conn.ReserveNewRequest() {
			return conn
		}
	}
	return nil
}

func (p *connPool) nextRoundRobinSlot() int {
	n := len(p.conns)
	if n == 0 {
		return -1
	}
	return int(p.idx.Add(1)-1) % n
}

func (t *utlsRoundTripper) getOrCreateConnection(host, addr string) (h2ClientConn, error) {
	for {
		t.mu.Lock()

		pool := t.poolForHostLocked(host)
		if h2Conn := pool.reserveRoundRobin(); h2Conn != nil {
			t.mu.Unlock()
			return h2Conn, nil
		}

		if cond, ok := t.pending[host]; ok {
			cond.Wait()
			t.mu.Unlock()
			continue
		}

		cond := sync.NewCond(&t.mu)
		t.pending[host] = cond
		t.mu.Unlock()

		h2Conn, err := t.dialConnection(host, addr)

		t.mu.Lock()
		pool = t.poolForHostLocked(host)
		delete(t.pending, host)
		cond.Broadcast()

		if err != nil {
			t.mu.Unlock()
			return nil, err
		}

		if existing := pool.reserveRoundRobin(); existing != nil {
			t.mu.Unlock()
			go func() { _ = h2Conn.Close() }()
			return existing, nil
		}

		if !h2Conn.ReserveNewRequest() {
			t.mu.Unlock()
			go func() { _ = h2Conn.Close() }()
			return nil, errors.New("utls: new HTTP/2 connection cannot accept a request")
		}

		if len(pool.conns) < t.poolSize {
			pool.conns = append(pool.conns, h2Conn)
			t.mu.Unlock()
			return h2Conn, nil
		}

		slot := pool.nextRoundRobinSlot()
		if slot < 0 {
			pool.conns = append(pool.conns, h2Conn)
			t.mu.Unlock()
			return h2Conn, nil
		}

		displaced := pool.conns[slot]
		pool.conns[slot] = h2Conn
		t.startDrain(pool, displaced)
		t.mu.Unlock()
		return h2Conn, nil
	}
}

func (t *utlsRoundTripper) dialConnection(host, addr string) (h2ClientConn, error) {
	if t.createConn != nil {
		return t.createConn(host, addr)
	}
	return t.createConnection(host, addr)
}

func (t *utlsRoundTripper) createConnection(host, addr string) (h2ClientConn, error) {
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

func (t *utlsRoundTripper) startDrain(pool *connPool, conn h2ClientConn) {
	pool.draining.Add(1)
	pool.drainWG.Add(1)
	go t.drainConn(pool, conn)
}

func (t *utlsRoundTripper) drainConn(pool *connPool, conn h2ClientConn) {
	defer pool.draining.Add(-1)
	defer pool.drainWG.Done()
	// A healthy Codex stream can run for a long time. Per AGENTS.md, cpa
	// must not invent an upstream timeout after the connection is established.
	_ = conn.Shutdown(context.Background())
	_ = conn.Close()
}

// maybeFillPool asynchronously prewarms the remaining selectable connections
// for a host. Without this, a healthy HTTP/2 connection can accept many streams,
// so an N-sized pool usually stays at one connection and provides no failure
// isolation under ordinary concurrency.
func (t *utlsRoundTripper) maybeFillPool(host, addr string) {
	if t == nil || t.poolSize <= 1 {
		return
	}
	t.mu.Lock()
	pool := t.poolForHostLocked(host)
	if len(pool.conns) >= t.poolSize {
		t.mu.Unlock()
		return
	}
	if t.filling == nil {
		t.filling = make(map[string]bool)
	}
	if t.filling[host] {
		t.mu.Unlock()
		return
	}
	t.filling[host] = true
	t.mu.Unlock()

	go t.fillPool(host, addr)
}

func (t *utlsRoundTripper) fillPool(host, addr string) {
	defer func() {
		t.mu.Lock()
		delete(t.filling, host)
		t.mu.Unlock()
	}()

	for {
		t.mu.Lock()
		pool := t.poolForHostLocked(host)
		if len(pool.conns) >= t.poolSize {
			t.mu.Unlock()
			return
		}
		t.mu.Unlock()

		conn, errDial := t.dialConnection(host, addr)
		if errDial != nil {
			log.Debugf("utls: failed to prewarm connection for %s: %v", host, errDial)
			return
		}

		t.mu.Lock()
		pool = t.poolForHostLocked(host)
		if len(pool.conns) >= t.poolSize {
			t.mu.Unlock()
			go func() { _ = conn.Close() }()
			return
		}
		pool.conns = append(pool.conns, conn)
		t.mu.Unlock()
	}
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
	if err != nil && isRetryableFreshConnError(err) && canResetBody(req) {
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
func (t *utlsRoundTripper) attempt(req *http.Request, hostname, addr string) (*http.Response, h2ClientConn, error) {
	h2Conn, err := t.getOrCreateConnection(hostname, addr)
	if err != nil {
		return nil, nil, err
	}
	resp, err := h2Conn.RoundTrip(req)
	if err != nil {
		t.handleConnError(hostname, h2Conn, err)
		return nil, nil, err
	}
	t.maybeFillPool(hostname, addr)
	return resp, h2Conn, nil
}

// handleConnError applies the least disruptive action appropriate for err.
// Selected stream errors retire the connection from new work but let existing
// streams drain. Connection-level errors close it immediately. Request-local
// errors leave the shared connection untouched.
func (t *utlsRoundTripper) handleConnError(hostname string, conn h2ClientConn, err error) {
	disposition := connectionErrorDisposition(err)
	if disposition == connErrorKeep {
		return
	}

	var removedFrom *connPool
	t.mu.Lock()
	pool := t.pools[hostname]
	if pool != nil {
		for i, cached := range pool.conns {
			if cached == conn {
				pool.conns = append(pool.conns[:i], pool.conns[i+1:]...)
				removedFrom = pool
				break
			}
		}
	}
	t.mu.Unlock()

	if removedFrom == nil {
		return
	}
	if disposition == connErrorDrain {
		t.startDrain(removedFrom, conn)
		return
	}
	go func() { _ = conn.Close() }()
}

// wrapBodyForEvict replaces resp.Body so that any non-EOF read error triggers
// eviction of the underlying conn. Idempotent: the eviction fires at most once
// per response body, even with many concurrent readers.
func wrapBodyForEvict(resp *http.Response, t *utlsRoundTripper, hostname string, conn h2ClientConn) *http.Response {
	if resp == nil || resp.Body == nil {
		return resp
	}
	resp.Body = &evictOnReadErrBody{
		ReadCloser: resp.Body,
		onErr: func(err error) {
			t.handleConnError(hostname, conn, err)
		},
	}
	return resp
}

// evictOnReadErrBody wraps an HTTP response body. The first non-EOF Read error
// triggers onErr exactly once (via sync.Once).
type evictOnReadErrBody struct {
	io.ReadCloser
	once  sync.Once
	onErr func(error)
}

func (b *evictOnReadErrBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && err != io.EOF {
		b.once.Do(func() {
			if b.onErr != nil {
				b.onErr(err)
			}
		})
	}
	return n, err
}

type connErrorDisposition uint8

const (
	connErrorKeep connErrorDisposition = iota
	connErrorDrain
	connErrorClose
)

func connectionErrorDisposition(err error) connErrorDisposition {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return connErrorKeep
	}

	var streamErr http2.StreamError
	if errors.As(err, &streamErr) {
		switch streamErr.Code {
		case http2.ErrCodeProtocol, http2.ErrCodeInternal:
			return connErrorDrain
		default:
			// REFUSED_STREAM and other stream-local failures do not make the
			// underlying HTTP/2 connection unusable.
			return connErrorKeep
		}
	}

	var connErr http2.ConnectionError
	if errors.As(err, &connErr) {
		return connErrorClose
	}
	var goAwayErr http2.GoAwayError
	if errors.As(err, &goAwayErr) {
		return connErrorClose
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) || errors.Is(err, io.ErrUnexpectedEOF) {
		return connErrorClose
	}

	// Preserve narrow fallbacks for errors wrapped by older transport code
	// without their concrete HTTP/2 type.
	message := strings.ToUpper(err.Error())
	if strings.Contains(message, "STREAM ERROR") {
		if strings.Contains(message, "PROTOCOL_ERROR") || strings.Contains(message, "INTERNAL_ERROR") {
			return connErrorDrain
		}
		return connErrorKeep
	}
	if strings.Contains(message, "CLIENT CONNECTION FORCE CLOSED") ||
		strings.Contains(message, "CONNECTION RESET BY PEER") ||
		strings.Contains(message, "BROKEN PIPE") ||
		strings.Contains(message, "USE OF CLOSED NETWORK CONNECTION") ||
		strings.Contains(message, "UNEXPECTED EOF") ||
		strings.Contains(message, "SERVER SENT GOAWAY") ||
		strings.Contains(message, "CONNECTION ERROR") {
		return connErrorClose
	}
	return connErrorKeep
}

func isRetryableFreshConnError(err error) bool {
	if err == nil {
		return false
	}
	return isStreamLevelError(err) || errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), "unexpected EOF")
}

// isStreamLevelError matches errors that are safe to retry before any response
// bytes have been exposed. It does not decide whether the connection is usable.
func isStreamLevelError(err error) bool {
	if err == nil {
		return false
	}
	var streamErr http2.StreamError
	if errors.As(err, &streamErr) {
		switch streamErr.Code {
		case http2.ErrCodeProtocol, http2.ErrCodeInternal, http2.ErrCodeRefusedStream:
			return true
		default:
			return false
		}
	}
	message := strings.ToUpper(err.Error())
	return strings.Contains(message, "STREAM ERROR") &&
		(strings.Contains(message, "PROTOCOL_ERROR") ||
			strings.Contains(message, "INTERNAL_ERROR") ||
			strings.Contains(message, "REFUSED_STREAM"))
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

func cachedRoundTrippers(proxyURL string, poolSize int) cachedUtlsRoundTrippers {
	normalizedPoolSize := normalizeUtlsPoolSize(poolSize)
	key := utlsRoundTripperCacheKey{
		proxyURL: proxyURL,
		poolSize: normalizedPoolSize,
	}

	utlsRoundTripperCache.mu.RLock()
	cached, ok := utlsRoundTripperCache.items[key]
	utlsRoundTripperCache.mu.RUnlock()
	if ok {
		return cached
	}

	cached = cachedUtlsRoundTrippers{
		utls:     newUtlsRoundTripper(proxyURL, normalizedPoolSize),
		fallback: http.DefaultTransport,
	}
	if proxyURL != "" {
		if transport := buildProxyTransport(proxyURL); transport != nil {
			cached.fallback = transport
		}
	}

	utlsRoundTripperCache.mu.Lock()
	defer utlsRoundTripperCache.mu.Unlock()
	if existing, ok := utlsRoundTripperCache.items[key]; ok {
		return existing
	}
	if len(utlsRoundTripperCache.items) >= maxCachedUtlsRoundTrippers {
		return cached
	}
	utlsRoundTripperCache.items[key] = cached
	return cached
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

	var roundTrippers cachedUtlsRoundTrippers
	if proxyURL == "" && ctxRoundTripper != nil {
		roundTrippers = cachedUtlsRoundTrippers{
			utls:     ctxRoundTripper,
			fallback: ctxRoundTripper,
		}
	} else {
		roundTrippers = cachedRoundTrippers(proxyURL, effectiveUtlsPoolSize(cfg))
	}

	client := &http.Client{
		Transport: &fallbackRoundTripper{
			utls:     roundTrippers.utls,
			fallback: roundTrippers.fallback,
		},
	}
	if timeout > 0 {
		client.Timeout = timeout
	}
	return client
}
