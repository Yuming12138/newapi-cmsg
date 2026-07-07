package helps

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

type utlsClientRoundTripFunc func(*http.Request) (*http.Response, error)

func (f utlsClientRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNewUtlsHTTPClientUsesContextRoundTripperForProtectedHost(t *testing.T) {
	t.Parallel()

	called := false
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", utlsClientRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		if req.URL.Hostname() != "chatgpt.com" {
			t.Fatalf("hostname = %q, want chatgpt.com", req.URL.Hostname())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("{}")),
			Request:    req,
		}, nil
	}))

	client := NewUtlsHTTPClient(ctx, nil, nil, 0)
	resp, err := client.Get("https://chatgpt.com/backend-api/codex/responses")
	if err != nil {
		t.Fatalf("client.Get returned error: %v", err)
	}
	if errClose := resp.Body.Close(); errClose != nil {
		t.Fatalf("response body close returned error: %v", errClose)
	}
	if !called {
		t.Fatal("expected context RoundTripper to handle protected host request")
	}
}

func TestEffectiveUtlsPoolSize(t *testing.T) {
	t.Parallel()

	if got := normalizeUtlsPoolSize(0); got != defaultUtlsPoolSize {
		t.Fatalf("normalizeUtlsPoolSize(0) = %d, want %d", got, defaultUtlsPoolSize)
	}
	if got := normalizeUtlsPoolSize(-2); got != defaultUtlsPoolSize {
		t.Fatalf("normalizeUtlsPoolSize(-2) = %d, want %d", got, defaultUtlsPoolSize)
	}
	if got := normalizeUtlsPoolSize(3); got != 3 {
		t.Fatalf("normalizeUtlsPoolSize(3) = %d, want 3", got)
	}
	if got := effectiveUtlsPoolSize(&config.Config{SDKConfig: config.SDKConfig{UtlsPoolSize: 2}}); got != 2 {
		t.Fatalf("effectiveUtlsPoolSize() = %d, want 2", got)
	}
}

func TestNewUtlsHTTPClientReusesCachedRoundTrippers(t *testing.T) {
	resetUtlsRoundTripperCache(t)

	first := NewUtlsHTTPClient(context.Background(), nil, nil, 0)
	second := NewUtlsHTTPClient(context.Background(), nil, nil, 0)

	firstUtls, firstFallback := utlsClientRoundTrippers(t, first)
	secondUtls, secondFallback := utlsClientRoundTrippers(t, second)
	if firstUtls != secondUtls {
		t.Fatal("expected default uTLS RoundTripper to be cached")
	}
	if firstFallback != secondFallback {
		t.Fatal("expected default fallback RoundTripper to be cached")
	}
}

func TestNewUtlsHTTPClientCacheSeparatesPoolSize(t *testing.T) {
	resetUtlsRoundTripperCache(t)

	cfgOne := &config.Config{SDKConfig: config.SDKConfig{UtlsPoolSize: 1}}
	cfgTwo := &config.Config{SDKConfig: config.SDKConfig{UtlsPoolSize: 2}}

	first := NewUtlsHTTPClient(context.Background(), cfgOne, nil, 0)
	second := NewUtlsHTTPClient(context.Background(), cfgTwo, nil, 0)
	third := NewUtlsHTTPClient(context.Background(), cfgOne, nil, 0)

	firstUtls, _ := utlsClientRoundTrippers(t, first)
	secondUtls, _ := utlsClientRoundTrippers(t, second)
	thirdUtls, _ := utlsClientRoundTrippers(t, third)
	if firstUtls == secondUtls {
		t.Fatal("expected different pool sizes to use separate uTLS RoundTrippers")
	}
	if firstUtls != thirdUtls {
		t.Fatal("expected same pool size to reuse cached uTLS RoundTripper")
	}
}

func TestNewUtlsHTTPClientReusesCachedRoundTrippersForProxyKey(t *testing.T) {
	resetUtlsRoundTripperCache(t)

	cfg := &config.Config{SDKConfig: config.SDKConfig{ProxyURL: "direct", UtlsPoolSize: 2}}
	first := NewUtlsHTTPClient(context.Background(), cfg, nil, 0)
	second := NewUtlsHTTPClient(context.Background(), cfg, nil, 0)

	firstUtls, firstFallback := utlsClientRoundTrippers(t, first)
	secondUtls, secondFallback := utlsClientRoundTrippers(t, second)
	if firstUtls != secondUtls {
		t.Fatal("expected proxy-keyed uTLS RoundTripper to be cached")
	}
	if firstFallback != secondFallback {
		t.Fatal("expected proxy-keyed fallback RoundTripper to be cached")
	}
}

func TestUtlsRoundTripperSaturatedReplacementDrainsDisplacedConn(t *testing.T) {
	t.Parallel()

	first := newFakeH2Conn("first", 1)
	second := newFakeH2Conn("second", 1)
	replacement := newFakeH2Conn("replacement", 1)
	rt := newFakeUtlsRoundTripper(2, first, second, replacement)

	gotFirst, err := rt.getOrCreateConnection("chatgpt.com", "chatgpt.com:443")
	if err != nil {
		t.Fatalf("first getOrCreateConnection returned error: %v", err)
	}
	if gotFirst != first {
		t.Fatalf("first conn = %v, want first fake", gotFirst)
	}

	gotSecond, err := rt.getOrCreateConnection("chatgpt.com", "chatgpt.com:443")
	if err != nil {
		t.Fatalf("second getOrCreateConnection returned error: %v", err)
	}
	if gotSecond != second {
		t.Fatalf("second conn = %v, want second fake", gotSecond)
	}

	gotReplacement, err := rt.getOrCreateConnection("chatgpt.com", "chatgpt.com:443")
	if err != nil {
		t.Fatalf("replacement getOrCreateConnection returned error: %v", err)
	}
	if gotReplacement != replacement {
		t.Fatalf("replacement conn = %v, want replacement fake", gotReplacement)
	}

	rt.mu.Lock()
	pool := rt.pools["chatgpt.com"]
	conns := append([]h2ClientConn(nil), pool.conns...)
	rt.mu.Unlock()

	if len(conns) != 2 {
		t.Fatalf("pool size = %d, want 2", len(conns))
	}
	if !fakeConnSliceContains(conns, replacement) {
		t.Fatal("pool does not contain replacement conn")
	}

	var displaced *fakeH2Conn
	switch {
	case !fakeConnSliceContains(conns, first):
		displaced = first
	case !fakeConnSliceContains(conns, second):
		displaced = second
	default:
		t.Fatal("expected either first or second conn to be displaced")
	}

	pool.drainWG.Wait()
	if got := pool.draining.Load(); got != 0 {
		t.Fatalf("draining = %d, want 0", got)
	}
	if got := displaced.shutdownCalls(); got != 1 {
		t.Fatalf("displaced shutdown calls = %d, want 1", got)
	}
	if got := displaced.closeCalls(); got != 1 {
		t.Fatalf("displaced close calls = %d, want 1", got)
	}
}

func TestUtlsRoundTripperEvictsAndDrainsOnlyPoisonedConn(t *testing.T) {
	t.Parallel()

	poisoned := newFakeH2Conn("poisoned", -1)
	poisoned.shutdownStarted = make(chan struct{})
	poisoned.shutdownRelease = make(chan struct{})
	sibling := newFakeH2Conn("sibling", -1)
	rt := newFakeUtlsRoundTripper(2)
	pool := &connPool{conns: []h2ClientConn{poisoned, sibling}}
	rt.pools["chatgpt.com"] = pool

	rt.evictConn("chatgpt.com", poisoned)

	select {
	case <-poisoned.shutdownStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for poisoned conn Shutdown to start")
	}

	rt.mu.Lock()
	conns := append([]h2ClientConn(nil), rt.pools["chatgpt.com"].conns...)
	rt.mu.Unlock()

	if len(conns) != 1 || conns[0] != sibling {
		t.Fatalf("remaining conns = %#v, want only sibling", conns)
	}
	if got := sibling.closeCalls(); got != 0 {
		t.Fatalf("sibling close calls = %d, want 0", got)
	}
	select {
	case <-poisoned.closeCh:
		t.Fatal("poisoned conn was closed before Shutdown returned")
	default:
	}

	close(poisoned.shutdownRelease)
	pool.drainWG.Wait()
	waitForFakeClose(t, poisoned)

	if got := poisoned.shutdownCalls(); got != 1 {
		t.Fatalf("poisoned shutdown calls = %d, want 1", got)
	}
	if got := poisoned.closeCalls(); got != 1 {
		t.Fatalf("poisoned close calls = %d, want 1", got)
	}
}

func TestUtlsRoundTripperDrainConnDoesNotCloseBeforeShutdownReturns(t *testing.T) {
	t.Parallel()

	conn := newFakeH2Conn("draining", -1)
	conn.shutdownStarted = make(chan struct{})
	conn.shutdownRelease = make(chan struct{})
	pool := &connPool{}
	rt := newFakeUtlsRoundTripper(1)

	rt.startDrain(pool, conn)

	select {
	case <-conn.shutdownStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Shutdown to start")
	}

	if got := pool.draining.Load(); got != 1 {
		t.Fatalf("draining = %d, want 1 while Shutdown is blocked", got)
	}
	select {
	case <-conn.closeCh:
		t.Fatal("Close was called before Shutdown returned")
	default:
	}

	close(conn.shutdownRelease)
	pool.drainWG.Wait()
	waitForFakeClose(t, conn)

	if got := pool.draining.Load(); got != 0 {
		t.Fatalf("draining = %d, want 0 after drain finishes", got)
	}
	if got := conn.shutdownCalls(); got != 1 {
		t.Fatalf("shutdown calls = %d, want 1", got)
	}
	if got := conn.closeCalls(); got != 1 {
		t.Fatalf("close calls = %d, want 1", got)
	}
}

func newFakeUtlsRoundTripper(poolSize int, dialed ...*fakeH2Conn) *utlsRoundTripper {
	var mu sync.Mutex
	queue := append([]*fakeH2Conn(nil), dialed...)
	return &utlsRoundTripper{
		pools:    make(map[string]*connPool),
		pending:  make(map[string]*sync.Cond),
		poolSize: normalizeUtlsPoolSize(poolSize),
		createConn: func(_, _ string) (h2ClientConn, error) {
			mu.Lock()
			defer mu.Unlock()
			if len(queue) == 0 {
				return nil, errors.New("no fake connection queued")
			}
			conn := queue[0]
			queue = queue[1:]
			return conn, nil
		},
	}
}

type fakeH2Conn struct {
	name            string
	maxReservations int

	mu                sync.Mutex
	reserved          int
	roundTripCallsNum int
	shutdownCallsNum  int
	closeCallsNum     int
	closeOnce         sync.Once

	roundTripErr    error
	shutdownErr     error
	shutdownStarted chan struct{}
	shutdownRelease chan struct{}
	closeCh         chan struct{}
}

func newFakeH2Conn(name string, maxReservations int) *fakeH2Conn {
	return &fakeH2Conn{
		name:            name,
		maxReservations: maxReservations,
		closeCh:         make(chan struct{}),
	}
}

func (f *fakeH2Conn) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	if f.reserved > 0 {
		f.reserved--
	}
	f.roundTripCallsNum++
	err := f.roundTripErr
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("{}")),
		Request:    req,
	}, nil
}

func (f *fakeH2Conn) ReserveNewRequest() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.maxReservations >= 0 && f.reserved >= f.maxReservations {
		return false
	}
	f.reserved++
	return true
}

func (f *fakeH2Conn) Shutdown(context.Context) error {
	f.mu.Lock()
	f.shutdownCallsNum++
	started := f.shutdownStarted
	release := f.shutdownRelease
	err := f.shutdownErr
	f.mu.Unlock()

	if started != nil {
		close(started)
	}
	if release != nil {
		<-release
	}
	return err
}

func (f *fakeH2Conn) Close() error {
	f.mu.Lock()
	f.closeCallsNum++
	f.mu.Unlock()
	f.closeOnce.Do(func() {
		close(f.closeCh)
	})
	return nil
}

func (f *fakeH2Conn) shutdownCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shutdownCallsNum
}

func (f *fakeH2Conn) closeCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeCallsNum
}

func fakeConnSliceContains(conns []h2ClientConn, want *fakeH2Conn) bool {
	for _, conn := range conns {
		if conn == want {
			return true
		}
	}
	return false
}

func waitForFakeClose(t *testing.T, conn *fakeH2Conn) {
	t.Helper()
	select {
	case <-conn.closeCh:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s to close", conn.name)
	}
}

func resetUtlsRoundTripperCache(t *testing.T) {
	t.Helper()

	utlsRoundTripperCache.mu.Lock()
	oldItems := utlsRoundTripperCache.items
	utlsRoundTripperCache.items = make(map[utlsRoundTripperCacheKey]cachedUtlsRoundTrippers)
	utlsRoundTripperCache.mu.Unlock()

	t.Cleanup(func() {
		utlsRoundTripperCache.mu.Lock()
		utlsRoundTripperCache.items = oldItems
		utlsRoundTripperCache.mu.Unlock()
	})
}

func utlsClientRoundTrippers(t *testing.T, client *http.Client) (http.RoundTripper, http.RoundTripper) {
	t.Helper()

	transport, ok := client.Transport.(*fallbackRoundTripper)
	if !ok {
		t.Fatalf("client.Transport type = %T, want *fallbackRoundTripper", client.Transport)
	}
	return transport.utls, transport.fallback
}
