package helps

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func TestClassifyTransportFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		err   error
		phase transportFailurePhase
		want  transportFailureClass
	}{
		{name: "canceled", err: context.Canceled, phase: transportPhaseResponseBody, want: transportFailureCanceled},
		{name: "deadline", err: context.DeadlineExceeded, phase: transportPhaseResponseBody, want: transportFailureDeadline},
		{name: "proxy timeout", err: shadowTimeoutError{}, phase: transportPhaseProxyConnect, want: transportFailureProxyTimeout},
		{name: "connect gateway", err: errors.New("proxy CONNECT 504"), phase: transportPhaseProxyConnect, want: transportFailureProxyGateway},
		{name: "gateway status", err: errors.New("unexpected status: 502 Bad Gateway"), phase: transportPhaseProxyConnect, want: transportFailureProxyGateway},
		{name: "tls", err: errors.New("remote error: tls handshake failure"), phase: transportPhaseTLSHandshake, want: transportFailureTLS},
		{name: "client conn establish", err: errors.New("http2: client conn could not be established"), phase: transportPhaseRequestHeaders, want: transportFailureH2Establish},
		{name: "protocol stream", err: http2.StreamError{StreamID: 1, Code: http2.ErrCodeProtocol}, phase: transportPhaseResponseBody, want: transportFailureH2Protocol},
		{name: "internal stream", err: http2.StreamError{StreamID: 3, Code: http2.ErrCodeInternal}, phase: transportPhaseResponseBody, want: transportFailureH2Internal},
		{name: "refused stream", err: http2.StreamError{StreamID: 5, Code: http2.ErrCodeRefusedStream}, phase: transportPhaseRequestHeaders, want: transportFailureH2RefusedStream},
		{name: "connection", err: http2.ConnectionError(http2.ErrCodeProtocol), phase: transportPhaseRequestHeaders, want: transportFailureH2Connection},
		{name: "unexpected eof", err: io.ErrUnexpectedEOF, phase: transportPhaseResponseBody, want: transportFailureUnexpectedEOF},
		{name: "force closed", err: errors.New("http2: client connection force closed via Close"), phase: transportPhaseResponseBody, want: transportFailureNetwork},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyTransportFailure(test.err, test.phase); got != test.want {
				t.Fatalf("classifyTransportFailure() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTransportShadowObserverRequiresTwoDistinctH2Connections(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	observer := newTransportShadowObserver()
	observer.now = func() time.Time { return now }
	trace := NewTransportShadowTrace()
	ctx := WithTransportShadowTrace(context.Background(), trace)
	input := transportFailureInput{
		Host:           "chatgpt.com",
		ProxyRoute:     "http://mihomo:7890",
		ConnectionID:   11,
		PoolGeneration: 4,
		Phase:          transportPhaseResponseBody,
		Err:            http2.StreamError{StreamID: 1, Code: http2.ErrCodeProtocol},
		HasConnection:  true,
		RetryAttempt:   0,
		RetryBudget:    1,
	}

	first := observer.observeFailure(ctx, input)
	if first.ShadowAction != transportActionMarkSuspect {
		t.Fatalf("first shadow action = %q, want mark suspect", first.ShadowAction)
	}
	if !first.ShadowReplayEligible || !first.PayloadBoundaryKnown || first.PayloadCommitted {
		t.Fatalf("first payload state = %#v, want known uncommitted replay eligibility", first)
	}

	sameConnection := observer.observeFailure(ctx, input)
	if sameConnection.ShadowAction != transportActionMarkSuspect {
		t.Fatalf("same-connection shadow action = %q, want mark suspect", sameConnection.ShadowAction)
	}

	input.ConnectionID = 12
	secondConnection := observer.observeFailure(ctx, input)
	if secondConnection.ShadowAction != transportActionSwitchNode {
		t.Fatalf("second-connection shadow action = %q, want switch node", secondConnection.ShadowAction)
	}
	if secondConnection.ShadowSwitchTotal != 1 {
		t.Fatalf("shadow switch total = %d, want 1", secondConnection.ShadowSwitchTotal)
	}
	if secondConnection.SelectedNode != "unknown" || secondConnection.SelectedNodeSource != "controller_not_configured" {
		t.Fatalf("selected node snapshot = %q/%q", secondConnection.SelectedNode, secondConnection.SelectedNodeSource)
	}

	trace.MarkPayloadCommitted()
	input.ConnectionID = 13
	committed := observer.observeFailure(ctx, input)
	if !committed.PayloadCommitted || committed.ShadowReplayEligible {
		t.Fatalf("committed payload state = %#v, want replay disabled", committed)
	}
}

func TestTransportShadowObserverActionsMatchRecoveryPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		err           error
		phase         transportFailurePhase
		hasConnection bool
		want          transportAction
	}{
		{name: "proxy timeout switches", err: shadowTimeoutError{}, phase: transportPhaseProxyConnect, want: transportActionSwitchNode},
		{name: "connect gateway switches", err: errors.New("proxy CONNECT 502"), phase: transportPhaseProxyConnect, want: transportActionSwitchNode},
		{name: "tls failure switches", err: errors.New("tls handshake failed"), phase: transportPhaseTLSHandshake, want: transportActionSwitchNode},
		{name: "client conn establishment switches", err: errors.New("http2: client conn could not be established"), phase: transportPhaseRequestHeaders, hasConnection: true, want: transportActionSwitchNode},
		{name: "refused stream replaces connection", err: http2.StreamError{StreamID: 7, Code: http2.ErrCodeRefusedStream}, phase: transportPhaseRequestHeaders, hasConnection: true, want: transportActionReplaceConnection},
		{name: "cancellation has no route action", err: context.Canceled, phase: transportPhaseResponseBody, hasConnection: true, want: transportActionNone},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			observer := newTransportShadowObserver()
			observation := observer.observeFailure(context.Background(), transportFailureInput{
				Host:           "chatgpt.com",
				ProxyRoute:     "http://mihomo:7890",
				ConnectionID:   31,
				PoolGeneration: 2,
				Phase:          test.phase,
				Err:            test.err,
				HasConnection:  test.hasConnection,
				RetryAttempt:   0,
				RetryBudget:    1,
			})
			if observation.ShadowAction != test.want {
				t.Fatalf("shadow action = %q, want %q", observation.ShadowAction, test.want)
			}
		})
	}
}

func TestTransportFailureObservationMarksPendingControllerSnapshot(t *testing.T) {
	t.Parallel()

	observer := newTransportShadowObserver()
	observation := observer.observeFailure(context.Background(), transportFailureInput{
		Host:                 "chatgpt.com",
		ProxyRoute:           "http://mihomo:7890",
		RouteRecoveryEnabled: true,
		ConnectionID:         31,
		PoolGeneration:       2,
		Phase:                transportPhaseResponseBody,
		Err:                  http2.StreamError{StreamID: 7, Code: http2.ErrCodeProtocol},
		HasConnection:        true,
		RetryAttempt:         0,
		RetryBudget:          1,
	})
	if observation.SelectedNode != "unknown" || observation.SelectedNodeSource != "controller_snapshot_pending" {
		t.Fatalf("selected node snapshot = %q/%q", observation.SelectedNode, observation.SelectedNodeSource)
	}
}

func TestTransportShadowObserverExpiresH2FailureWindow(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	observer := newTransportShadowObserver()
	observer.now = func() time.Time { return now }
	input := transportFailureInput{
		Host:          "chatgpt.com",
		ProxyRoute:    "http://mihomo:7890",
		ConnectionID:  21,
		Phase:         transportPhaseResponseBody,
		Err:           http2.StreamError{StreamID: 1, Code: http2.ErrCodeInternal},
		HasConnection: true,
		RetryAttempt:  0,
		RetryBudget:   1,
	}
	if got := observer.observeFailure(context.Background(), input).ShadowAction; got != transportActionMarkSuspect {
		t.Fatalf("first shadow action = %q, want mark suspect", got)
	}

	now = now.Add(transportShadowH2ErrorWindow + time.Millisecond)
	input.ConnectionID = 22
	if got := observer.observeFailure(context.Background(), input).ShadowAction; got != transportActionMarkSuspect {
		t.Fatalf("expired-window shadow action = %q, want mark suspect", got)
	}
}

func TestTransportShadowObserverDoesNotMixPoolGenerations(t *testing.T) {
	t.Parallel()

	observer := newTransportShadowObserver()
	input := transportFailureInput{
		Host:           "chatgpt.com",
		ProxyRoute:     "http://mihomo:7890",
		ConnectionID:   41,
		PoolGeneration: 7,
		Phase:          transportPhaseResponseBody,
		Err:            http2.StreamError{StreamID: 1, Code: http2.ErrCodeProtocol},
		HasConnection:  true,
		RetryAttempt:   0,
		RetryBudget:    1,
	}
	if got := observer.observeFailure(context.Background(), input).ShadowAction; got != transportActionMarkSuspect {
		t.Fatalf("first generation action = %q, want mark suspect", got)
	}

	input.ConnectionID = 42
	input.PoolGeneration = 8
	if got := observer.observeFailure(context.Background(), input).ShadowAction; got != transportActionMarkSuspect {
		t.Fatalf("new generation action = %q, want mark suspect", got)
	}
}

func TestTransportShadowObserverDeduplicatesConcurrentSwitches(t *testing.T) {
	t.Parallel()

	observer := newTransportShadowObserver()
	var switchActions atomic.Int32
	var holdActions atomic.Int32
	observer.onFailure = func(observation transportFailureObservation) {
		switch observation.ShadowAction {
		case transportActionSwitchNode:
			switchActions.Add(1)
		case transportActionRouteHold:
			holdActions.Add(1)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(connID uint64) {
			defer wg.Done()
			observer.observeFailure(context.Background(), transportFailureInput{
				Host:           "chatgpt.com",
				ProxyRoute:     "http://mihomo:7890",
				ConnectionID:   connID,
				PoolGeneration: 9,
				Phase:          transportPhaseResponseBody,
				Err:            http2.StreamError{StreamID: uint32(connID*2 + 1), Code: http2.ErrCodeProtocol},
				HasConnection:  true,
				RetryAttempt:   0,
				RetryBudget:    1,
			})
		}(uint64(i + 1))
	}
	wg.Wait()

	if got := switchActions.Load(); got != 1 {
		t.Fatalf("switch actions = %d, want 1", got)
	}
	if got := holdActions.Load(); got == 0 {
		t.Fatal("route hold actions = 0, want concurrent switches suppressed")
	}
}

func TestUtlsRoundTripperBodyFailureSeesCommittedPayload(t *testing.T) {
	t.Parallel()

	conn := newFakeH2Conn("body-protocol-after-payload", -1)
	conn.responseBody = &payloadThenErrorCloser{
		payload: []byte("data: {\"type\":\"response.output_text.delta\"}\n\n"),
		err:     http2.StreamError{StreamID: 7, Code: http2.ErrCodeProtocol},
	}
	rt := newFakeUtlsRoundTripper(1)
	pool := &connPool{conns: []h2ClientConn{conn}}
	rt.pools["chatgpt.com"] = pool

	var observed transportFailureObservation
	rt.observer.onFailure = func(observation transportFailureObservation) {
		observed = observation
	}
	trace := NewTransportShadowTrace()
	ctx := WithTransportShadowTrace(context.Background(), trace)
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/backend-api/codex/responses", nil)
	if errRequest != nil {
		t.Fatalf("NewRequestWithContext() error = %v", errRequest)
	}
	resp, errRoundTrip := rt.RoundTrip(req)
	if errRoundTrip != nil {
		t.Fatalf("RoundTrip() error = %v", errRoundTrip)
	}

	buf := make([]byte, 128)
	if n, errRead := resp.Body.Read(buf); n == 0 || errRead != nil {
		t.Fatalf("first Read() = %d, %v, want payload", n, errRead)
	}
	trace.MarkPayloadCommitted()
	if _, errRead := resp.Body.Read(buf); errRead == nil {
		t.Fatal("second Read() error = nil, want protocol error")
	}
	pool.drainWG.Wait()

	if !observed.PayloadBoundaryKnown || !observed.PayloadCommitted || observed.ShadowReplayEligible {
		t.Fatalf("body failure payload state = %#v, want committed and not replayable", observed)
	}
	if observed.Phase != transportPhaseResponseBody || observed.ConnectionID == 0 {
		t.Fatalf("body failure metadata = %#v", observed)
	}
}

type shadowTimeoutError struct{}

func (shadowTimeoutError) Error() string   { return "proxy connect timeout" }
func (shadowTimeoutError) Timeout() bool   { return true }
func (shadowTimeoutError) Temporary() bool { return true }

type payloadThenErrorCloser struct {
	payload []byte
	err     error
	sent    bool
}

func (r *payloadThenErrorCloser) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, r.payload), nil
	}
	return 0, r.err
}

func (r *payloadThenErrorCloser) Close() error { return nil }
