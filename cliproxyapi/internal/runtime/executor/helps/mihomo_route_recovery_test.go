package helps

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"golang.org/x/net/http2"
)

func TestMihomoRouteControllerReadsGroupAndSelectsAuthenticatedNode(t *testing.T) {
	t.Parallel()

	secretPath := filepath.Join(t.TempDir(), "controller-secret")
	if errWrite := os.WriteFile(secretPath, []byte("test-secret\n"), 0o600); errWrite != nil {
		t.Fatalf("write secret: %v", errWrite)
	}

	var selected atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-secret" {
			t.Errorf("Authorization = %q, want bearer secret", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxies/OpenAI稳定":
			_, _ = w.Write([]byte(`{"name":"OpenAI稳定","now":"新加坡1","all":["新加坡1","美国1","日本1"]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/proxies/美国1":
			_, _ = w.Write([]byte(`{"alive":true}`))
		case r.Method == http.MethodPut && r.URL.Path == "/proxies/OpenAI稳定":
			body := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(body)
			selected.Store(string(body))
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	settings := routeRecoveryTestSettings()
	settings.controllerURL = server.URL
	settings.controllerSecretFile = secretPath
	controller := newMihomoRouteController(settings)

	group, errGroup := controller.Group(context.Background())
	if errGroup != nil {
		t.Fatalf("Group() error = %v", errGroup)
	}
	if group.Now != "新加坡1" || len(group.All) != 3 {
		t.Fatalf("Group() = %#v", group)
	}
	alive, errAlive := controller.NodeAlive(context.Background(), "美国1")
	if errAlive != nil || !alive {
		t.Fatalf("NodeAlive() = %t, %v", alive, errAlive)
	}
	if errSelect := controller.SelectNode(context.Background(), "美国1"); errSelect != nil {
		t.Fatalf("SelectNode() error = %v", errSelect)
	}
	if got, _ := selected.Load().(string); !strings.Contains(got, `"name":"美国1"`) {
		t.Fatalf("selection body = %q", got)
	}
}

func TestTransportRecoveryConcurrentFailuresIssueOneControllerPut(t *testing.T) {
	t.Parallel()

	controller := newFakeProxyRouteController("新加坡1", []string{"新加坡1", "美国1", "日本1"})
	controller.selectDelay = 25 * time.Millisecond
	rt := newRecoveryTestRoundTripper(controller)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(connID uint64) {
			defer wg.Done()
			rt.observer.observeFailure(context.Background(), transportFailureInput{
				Host:           "chatgpt.com",
				ProxyRoute:     "http://mihomo:7890",
				ConnectionID:   connID,
				PoolGeneration: 4,
				Phase:          transportPhaseRequestHeaders,
				Err:            errors.New("http2: client conn could not be established"),
				HasConnection:  true,
				RetryAttempt:   0,
				RetryBudget:    1,
			})
		}(uint64(i + 1))
	}
	wg.Wait()

	if got := controller.selectCalls.Load(); got != 1 {
		t.Fatalf("controller SelectNode calls = %d, want 1", got)
	}
	if got := rt.selectedNodeSnapshot(); got != "美国1" {
		t.Fatalf("selected node = %q, want 美国1", got)
	}
	if got := rt.hostGeneration("chatgpt.com"); got != 1 {
		t.Fatalf("pool generation = %d, want 1", got)
	}
}

func TestTransportRecoveryRequiresTwoDistinctH2Connections(t *testing.T) {
	t.Parallel()

	controller := newFakeProxyRouteController("新加坡1", []string{"新加坡1", "美国1"})
	rt := newRecoveryTestRoundTripper(controller)
	input := transportFailureInput{
		Host:           "chatgpt.com",
		ProxyRoute:     "http://mihomo:7890",
		ConnectionID:   11,
		PoolGeneration: 3,
		Phase:          transportPhaseResponseBody,
		Err:            http2.StreamError{StreamID: 1, Code: http2.ErrCodeProtocol},
		HasConnection:  true,
		RetryAttempt:   0,
		RetryBudget:    1,
	}

	rt.observer.observeFailure(context.Background(), input)
	rt.observer.observeFailure(context.Background(), input)
	if got := controller.selectCalls.Load(); got != 0 {
		t.Fatalf("same-connection SelectNode calls = %d, want 0", got)
	}
	input.ConnectionID = 12
	rt.observer.observeFailure(context.Background(), input)
	if got := controller.selectCalls.Load(); got != 1 {
		t.Fatalf("distinct-connection SelectNode calls = %d, want 1", got)
	}
}

func TestTransportRecoverySwitchesForDistinctH2ConnectionsFiftySecondsApart(t *testing.T) {
	t.Parallel()

	controller := newFakeProxyRouteController("新加坡1", []string{"新加坡1", "美国1"})
	rt := newRecoveryTestRoundTripper(controller)
	now := time.Unix(1_800_000_000, 0)
	rt.observer.now = func() time.Time { return now }
	input := transportFailureInput{
		Host:           "chatgpt.com",
		ProxyRoute:     "http://mihomo:7890",
		ConnectionID:   21,
		PoolGeneration: 3,
		Phase:          transportPhaseRequestHeaders,
		Err:            http2.StreamError{StreamID: 1, Code: http2.ErrCodeProtocol},
		HasConnection:  true,
		RetryAttempt:   0,
		RetryBudget:    1,
	}

	rt.observer.observeFailure(context.Background(), input)
	now = now.Add(50 * time.Second)
	input.ConnectionID = 22
	rt.observer.observeFailure(context.Background(), input)

	if got := controller.selectCalls.Load(); got != 1 {
		t.Fatalf("SelectNode calls = %d, want 1 for failures 50 seconds apart", got)
	}
}

func TestRouteSwitchDiscardsStaleDialAndStartsNewGeneration(t *testing.T) {
	t.Parallel()

	stale := newFakeH2Conn("stale-route", -1)
	replacement := newFakeH2Conn("new-route", -1)
	dialStarted := make(chan struct{})
	releaseDial := make(chan struct{})
	var dialCalls atomic.Int32
	rt := newFakeUtlsRoundTripper(1)
	rt.createConn = func(_, _ string) (h2ClientConn, error) {
		switch dialCalls.Add(1) {
		case 1:
			close(dialStarted)
			<-releaseDial
			return stale, nil
		case 2:
			return replacement, nil
		default:
			return nil, errors.New("unexpected extra dial")
		}
	}

	firstResult := make(chan h2ClientConn, 1)
	firstErr := make(chan error, 1)
	go collectConnectionResult(rt, firstResult, firstErr)
	select {
	case <-dialStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stale dial")
	}

	if generation := rt.activateRouteSwitch("chatgpt.com", "美国1"); generation != 1 {
		t.Fatalf("route switch generation = %d, want 1", generation)
	}
	secondResult := make(chan h2ClientConn, 1)
	secondErr := make(chan error, 1)
	go collectConnectionResult(rt, secondResult, secondErr)
	select {
	case err := <-secondErr:
		t.Fatalf("new-route dial error = %v", err)
	case conn := <-secondResult:
		if conn != replacement {
			t.Fatalf("new-route conn = %v, want replacement", conn)
		}
	case <-time.After(time.Second):
		t.Fatal("new-route dial stayed blocked behind stale generation")
	}

	close(releaseDial)
	select {
	case err := <-firstErr:
		t.Fatalf("stale-dial caller error = %v", err)
	case conn := <-firstResult:
		if conn != replacement {
			t.Fatalf("stale-dial caller conn = %v, want replacement", conn)
		}
	case <-time.After(time.Second):
		t.Fatal("stale-dial caller did not resume")
	}
	waitForFakeClose(t, stale)
}

func TestRouteSwitchDrainsHealthyOldConnectionWithoutTimeout(t *testing.T) {
	t.Parallel()

	old := newFakeH2Conn("healthy-old-route", -1)
	old.shutdownStarted = make(chan struct{})
	old.shutdownRelease = make(chan struct{})
	rt := newFakeUtlsRoundTripper(1)
	pool := &connPool{conns: []h2ClientConn{old}}
	rt.pools["chatgpt.com"] = pool

	rt.activateRouteSwitch("chatgpt.com", "美国1")
	select {
	case <-old.shutdownStarted:
	case <-time.After(time.Second):
		t.Fatal("old route did not begin graceful drain")
	}
	select {
	case <-old.closeCh:
		t.Fatal("healthy old connection closed before Shutdown completed")
	case <-time.After(30 * time.Millisecond):
	}
	close(old.shutdownRelease)
	pool.drainWG.Wait()
	waitForFakeClose(t, old)
}

func TestRouteRecoveryControllerFailureUnblocksWaitingDials(t *testing.T) {
	t.Parallel()

	controller := newFakeProxyRouteController("新加坡1", []string{"新加坡1", "美国1"})
	controller.groupStarted = make(chan struct{})
	controller.groupRelease = make(chan struct{})
	controller.groupErr = errors.New("controller unavailable")
	coordinator := newProxyRouteRecoveryCoordinator(routeRecoveryTestSettings(), controller)

	recoveryDone := make(chan routeRecoveryResult, 1)
	go func() { recoveryDone <- coordinator.recover("chatgpt.com") }()
	select {
	case <-controller.groupStarted:
	case <-time.After(time.Second):
		t.Fatal("controller recovery did not start")
	}
	waiterDone := make(chan struct{})
	go func() {
		coordinator.waitForStableRoute("chatgpt.com")
		close(waiterDone)
	}()
	select {
	case <-waiterDone:
		t.Fatal("dial waiter passed while route switch was active")
	case <-time.After(30 * time.Millisecond):
	}

	close(controller.groupRelease)
	select {
	case result := <-recoveryDone:
		if result.Err == nil || result.Switched {
			t.Fatalf("recovery result = %#v, want failed fallback", result)
		}
	case <-time.After(time.Second):
		t.Fatal("failed controller recovery did not return")
	}
	select {
	case <-waiterDone:
	case <-time.After(time.Second):
		t.Fatal("dial waiter remained blocked after controller failure")
	}
}

func TestFailedRouteRecoveryReleasesObserverHoldForLaterAttempt(t *testing.T) {
	t.Parallel()

	controller := newFakeProxyRouteController("新加坡1", []string{"新加坡1", "美国1"})
	controller.groupErr = errors.New("controller unavailable")
	rt := newRecoveryTestRoundTripper(controller)
	input := transportFailureInput{
		Host:           "chatgpt.com",
		ProxyRoute:     "http://mihomo:7890",
		ConnectionID:   1,
		PoolGeneration: 1,
		Phase:          transportPhaseRequestHeaders,
		Err:            errors.New("http2: client conn could not be established"),
		HasConnection:  true,
		RetryAttempt:   0,
		RetryBudget:    1,
	}

	rt.observer.observeFailure(context.Background(), input)
	input.ConnectionID = 2
	rt.observer.observeFailure(context.Background(), input)
	if got := controller.groupCalls.Load(); got != 2 {
		t.Fatalf("controller Group calls = %d, want a later recovery attempt after failure", got)
	}
}

func TestNewUtlsHTTPClientCacheSeparatesRouteRecoveryConfig(t *testing.T) {
	resetUtlsRoundTripperCache(t)

	base := &config.Config{SDKConfig: config.SDKConfig{
		ProxyURL:     "http://mihomo:7890",
		UtlsPoolSize: 2,
	}}
	enabled := base.CloneForRuntime()
	enabled.ProxyRouteRecovery = config.ProxyRouteRecoveryConfig{
		Enabled:              true,
		ControllerURL:        "http://mihomo:9090",
		ControllerSecretFile: "/app/secrets/mihomo-controller",
		Group:                "OpenAI稳定",
		Hosts:                []string{"chatgpt.com"},
	}

	baseClient := NewUtlsHTTPClient(context.Background(), base, nil, 0)
	enabledClient := NewUtlsHTTPClient(context.Background(), enabled, nil, 0)
	baseUtls, _ := utlsClientRoundTrippers(t, baseClient)
	enabledUtls, _ := utlsClientRoundTrippers(t, enabledClient)
	if baseUtls == enabledUtls {
		t.Fatal("different route recovery settings reused the same uTLS transport")
	}
}

func collectConnectionResult(rt *utlsRoundTripper, result chan<- h2ClientConn, errResult chan<- error) {
	conn, err := rt.getOrCreateConnection("chatgpt.com", "chatgpt.com:443")
	if err != nil {
		errResult <- err
		return
	}
	result <- conn
}

func routeRecoveryTestSettings() proxyRouteRecoverySettings {
	return proxyRouteRecoverySettings{
		enabled:                 true,
		controllerURL:           "http://mihomo:9090",
		controllerSecretFile:    "/tmp/test-secret",
		group:                   "OpenAI稳定",
		hosts:                   map[string]struct{}{"chatgpt.com": {}},
		h2ErrorWindow:           2 * time.Minute,
		h2ErrorThreshold:        2,
		nodeCooldown:            15 * time.Minute,
		repeatedFailureCooldown: 30 * time.Minute,
		routeHold:               10 * time.Minute,
		maxReplays:              1,
	}
}

func newRecoveryTestRoundTripper(controller proxyRouteController, dialed ...*fakeH2Conn) *utlsRoundTripper {
	settings := routeRecoveryTestSettings()
	rt := newFakeUtlsRoundTripper(1, dialed...)
	rt.observer = newTransportShadowObserverWithPolicy(settings.h2ErrorWindow, settings.h2ErrorThreshold, settings.routeHold)
	rt.routeRecovery = newProxyRouteRecoveryCoordinator(settings, controller)
	rt.routeRecovery.onNodeObserved = rt.setSelectedNode
	rt.routeRecovery.onSwitched = rt.activateRouteSwitch
	rt.observer.onFailure = func(observation transportFailureObservation) {
		if observation.ShadowAction == transportActionSwitchNode {
			result := rt.routeRecovery.recover(observation.Host)
			if result.Attempted && !result.Switched {
				rt.observer.releaseSwitchHold(observation.Host, observation.ProxyRoute, observation.SelectedNode)
			}
		}
	}
	return rt
}

type fakeProxyRouteController struct {
	mu sync.Mutex

	current      string
	nodes        []string
	alive        map[string]bool
	groupErr     error
	groupStarted chan struct{}
	groupRelease chan struct{}
	selectDelay  time.Duration
	groupCalls   atomic.Int32
	selectCalls  atomic.Int32
}

func newFakeProxyRouteController(current string, nodes []string) *fakeProxyRouteController {
	alive := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		alive[node] = true
	}
	return &fakeProxyRouteController{current: current, nodes: append([]string(nil), nodes...), alive: alive}
}

func (c *fakeProxyRouteController) Group(context.Context) (mihomoProxyGroup, error) {
	c.groupCalls.Add(1)
	if c.groupStarted != nil {
		close(c.groupStarted)
	}
	if c.groupRelease != nil {
		<-c.groupRelease
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return mihomoProxyGroup{Name: "OpenAI稳定", Now: c.current, All: append([]string(nil), c.nodes...)}, c.groupErr
}

func (c *fakeProxyRouteController) NodeAlive(_ context.Context, node string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.alive[node], nil
}

func (c *fakeProxyRouteController) SelectNode(_ context.Context, node string) error {
	c.selectCalls.Add(1)
	if c.selectDelay > 0 {
		time.Sleep(c.selectDelay)
	}
	c.mu.Lock()
	c.current = node
	c.mu.Unlock()
	return nil
}
