package helps

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
	"golang.org/x/net/http2"
)

const (
	transportShadowH2ErrorWindow = 30 * time.Second
	transportShadowRouteHold     = 10 * time.Minute
)

type transportFailurePhase string

const (
	transportPhaseConnectionAcquire transportFailurePhase = "connection_acquire"
	transportPhaseProxyConnect      transportFailurePhase = "proxy_connect"
	transportPhaseTLSHandshake      transportFailurePhase = "tls_handshake"
	transportPhaseH2Establish       transportFailurePhase = "h2_establish"
	transportPhaseRequestHeaders    transportFailurePhase = "request_headers"
	transportPhaseResponseBody      transportFailurePhase = "response_body"
)

type transportFailureClass string

const (
	transportFailureUnknown         transportFailureClass = "unknown"
	transportFailureCanceled        transportFailureClass = "request_canceled"
	transportFailureDeadline        transportFailureClass = "request_deadline"
	transportFailureProxyTimeout    transportFailureClass = "proxy_connect_timeout"
	transportFailureProxyGateway    transportFailureClass = "proxy_connect_gateway"
	transportFailureProxyConnect    transportFailureClass = "proxy_connect_failure"
	transportFailureTLS             transportFailureClass = "tls_failure"
	transportFailureH2Establish     transportFailureClass = "h2_client_establishment"
	transportFailureH2Protocol      transportFailureClass = "h2_protocol_error"
	transportFailureH2Internal      transportFailureClass = "h2_internal_error"
	transportFailureH2RefusedStream transportFailureClass = "h2_refused_stream"
	transportFailureH2Connection    transportFailureClass = "h2_connection_error"
	transportFailureUnexpectedEOF   transportFailureClass = "unexpected_eof"
	transportFailureNetwork         transportFailureClass = "network_failure"
)

type transportAction string

const (
	transportActionNone              transportAction = "none"
	transportActionDialFailed        transportAction = "dial_failed"
	transportActionKeepConnection    transportAction = "keep_connection"
	transportActionDrainConnection   transportAction = "drain_connection"
	transportActionCloseConnection   transportAction = "close_connection"
	transportActionRetireHostPool    transportAction = "retire_host_pool"
	transportActionMarkSuspect       transportAction = "mark_node_suspect"
	transportActionReplaceConnection transportAction = "replace_connection"
	transportActionSwitchNode        transportAction = "switch_node"
	transportActionRouteHold         transportAction = "route_hold"
)

type transportRetryOutcome string

const (
	transportRetrySucceeded           transportRetryOutcome = "succeeded"
	transportRetryFailed              transportRetryOutcome = "failed"
	transportRetryBodyResetFailed     transportRetryOutcome = "body_reset_failed"
	transportRetrySkippedUnreplayable transportRetryOutcome = "skipped_unreplayable"
)

type transportPhaseError struct {
	phase transportFailurePhase
	err   error
}

func (e *transportPhaseError) Error() string { return e.err.Error() }
func (e *transportPhaseError) Unwrap() error { return e.err }

func withTransportFailurePhase(phase transportFailurePhase, err error) error {
	if err == nil {
		return nil
	}
	return &transportPhaseError{phase: phase, err: err}
}

func transportFailurePhaseOf(err error, fallback transportFailurePhase) transportFailurePhase {
	var phased *transportPhaseError
	if errors.As(err, &phased) && phased.phase != "" {
		return phased.phase
	}
	return fallback
}

type transportShadowTraceKey struct{}

// TransportShadowTrace shares the meaningful-payload boundary between the
// Codex executor and transport error observer. Keep-alive/control events do not
// mark the trace committed.
type TransportShadowTrace struct {
	payloadCommitted atomic.Bool
}

func NewTransportShadowTrace() *TransportShadowTrace {
	return &TransportShadowTrace{}
}

func WithTransportShadowTrace(ctx context.Context, trace *TransportShadowTrace) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if trace == nil {
		return ctx
	}
	return context.WithValue(ctx, transportShadowTraceKey{}, trace)
}

func (t *TransportShadowTrace) MarkPayloadCommitted() {
	if t != nil {
		t.payloadCommitted.Store(true)
	}
}

// TransportShadowPayloadState reports whether a Codex stream has emitted its
// first meaningful payload and whether the request carries that boundary.
func TransportShadowPayloadState(ctx context.Context) (committed bool, known bool) {
	if ctx == nil {
		return false, false
	}
	trace, ok := ctx.Value(transportShadowTraceKey{}).(*TransportShadowTrace)
	if !ok || trace == nil {
		return false, false
	}
	return trace.payloadCommitted.Load(), true
}

type transportFailureObservation struct {
	Host                 string
	ProxyRoute           string
	SelectedNode         string
	SelectedNodeSource   string
	ConnectionID         uint64
	PoolGeneration       uint64
	Phase                transportFailurePhase
	Class                transportFailureClass
	ActualAction         transportAction
	ShadowAction         transportAction
	PayloadCommitted     bool
	PayloadBoundaryKnown bool
	ShadowReplayEligible bool
	RetryAttempt         int
	RetryBudget          int
	FailureClassTotal    uint64
	ShadowActionTotal    uint64
	ShadowSwitchTotal    uint64
}

type transportRetryObservation struct {
	Host              string
	ProxyRoute        string
	SelectedNode      string
	ConnectionID      uint64
	PoolGeneration    uint64
	RetryAttempt      int
	RetryBudget       int
	Outcome           transportRetryOutcome
	RetryOutcomeTotal uint64
}

type transportFailureInput struct {
	Host           string
	ProxyRoute     string
	SelectedNode   string
	ConnectionID   uint64
	PoolGeneration uint64
	Phase          transportFailurePhase
	Err            error
	HasConnection  bool
	RetryAttempt   int
	RetryBudget    int
}

type transportShadowObserver struct {
	mu sync.Mutex

	now              func() time.Time
	h2ErrorWindow    time.Duration
	h2ErrorThreshold int
	routeHold        time.Duration
	h2Failures       map[string]*transportH2FailureWindow
	routeHoldUntil   map[string]time.Time
	failureCounts    map[transportFailureClass]uint64
	actionCounts     map[transportAction]uint64
	retryCounts      map[transportRetryOutcome]uint64
	shadowSwitches   uint64

	onFailure func(transportFailureObservation)
	onRetry   func(transportRetryObservation)
}

type transportH2FailureWindow struct {
	generation  uint64
	connections map[uint64]time.Time
}

func newTransportShadowObserver() *transportShadowObserver {
	return newTransportShadowObserverWithPolicy(transportShadowH2ErrorWindow, 2, transportShadowRouteHold)
}

func newTransportShadowObserverWithPolicy(h2ErrorWindow time.Duration, h2ErrorThreshold int, routeHold time.Duration) *transportShadowObserver {
	if h2ErrorWindow <= 0 {
		h2ErrorWindow = transportShadowH2ErrorWindow
	}
	if h2ErrorThreshold <= 0 {
		h2ErrorThreshold = 2
	}
	if routeHold <= 0 {
		routeHold = transportShadowRouteHold
	}
	return &transportShadowObserver{
		now:              time.Now,
		h2ErrorWindow:    h2ErrorWindow,
		h2ErrorThreshold: h2ErrorThreshold,
		routeHold:        routeHold,
		h2Failures:       make(map[string]*transportH2FailureWindow),
		routeHoldUntil:   make(map[string]time.Time),
		failureCounts:    make(map[transportFailureClass]uint64),
		actionCounts:     make(map[transportAction]uint64),
		retryCounts:      make(map[transportRetryOutcome]uint64),
	}
}

func (o *transportShadowObserver) observeFailure(ctx context.Context, input transportFailureInput) transportFailureObservation {
	if o == nil {
		return transportFailureObservation{}
	}
	phase := transportFailurePhaseOf(input.Err, input.Phase)
	class := classifyTransportFailure(input.Err, phase)
	committed, boundaryKnown := TransportShadowPayloadState(ctx)

	o.mu.Lock()
	shadowAction := o.shadowActionLocked(input.Host, input.ProxyRoute, input.SelectedNode, input.ConnectionID, input.PoolGeneration, class)
	o.failureCounts[class]++
	o.actionCounts[shadowAction]++
	if shadowAction == transportActionSwitchNode {
		o.shadowSwitches++
	}
	observation := transportFailureObservation{
		Host:                 input.Host,
		ProxyRoute:           normalizedProxyRoute(input.ProxyRoute),
		SelectedNode:         normalizedSelectedNode(input.SelectedNode),
		SelectedNodeSource:   selectedNodeSource(input.SelectedNode),
		ConnectionID:         input.ConnectionID,
		PoolGeneration:       input.PoolGeneration,
		Phase:                phase,
		Class:                class,
		ActualAction:         actualTransportAction(input.HasConnection, input.Err),
		ShadowAction:         shadowAction,
		PayloadCommitted:     committed,
		PayloadBoundaryKnown: boundaryKnown,
		ShadowReplayEligible: shadowReplayEligible(class, committed, boundaryKnown, input.RetryAttempt, input.RetryBudget),
		RetryAttempt:         input.RetryAttempt,
		RetryBudget:          input.RetryBudget,
		FailureClassTotal:    o.failureCounts[class],
		ShadowActionTotal:    o.actionCounts[shadowAction],
		ShadowSwitchTotal:    o.shadowSwitches,
	}
	hook := o.onFailure
	o.mu.Unlock()

	if hook != nil {
		hook(observation)
	}
	logTransportFailure(ctx, observation)
	return observation
}

func (o *transportShadowObserver) observeRetry(ctx context.Context, observation transportRetryObservation) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.retryCounts[observation.Outcome]++
	observation.RetryOutcomeTotal = o.retryCounts[observation.Outcome]
	hook := o.onRetry
	o.mu.Unlock()

	observation.ProxyRoute = normalizedProxyRoute(observation.ProxyRoute)
	observation.SelectedNode = normalizedSelectedNode(observation.SelectedNode)
	if hook != nil {
		hook(observation)
	}
	logTransportRetry(ctx, observation)
}

func (o *transportShadowObserver) shadowActionLocked(host, proxyRoute, selectedNode string, connID, generation uint64, class transportFailureClass) transportAction {
	routeKey := host + "\x00" + proxyRoute + "\x00" + normalizedSelectedNode(selectedNode)
	switch class {
	case transportFailureProxyTimeout, transportFailureProxyGateway, transportFailureTLS, transportFailureH2Establish:
		return o.shadowSwitchActionLocked(routeKey, o.now())
	case transportFailureH2Protocol, transportFailureH2Internal:
		if connID == 0 {
			return transportActionMarkSuspect
		}
		now := o.now()
		window := o.h2Failures[routeKey]
		if window == nil || window.generation != generation {
			window = &transportH2FailureWindow{
				generation:  generation,
				connections: make(map[uint64]time.Time),
			}
			o.h2Failures[routeKey] = window
		}
		cutoff := now.Add(-o.h2ErrorWindow)
		for id, observedAt := range window.connections {
			if observedAt.Before(cutoff) {
				delete(window.connections, id)
			}
		}
		window.connections[connID] = now
		if len(window.connections) >= o.h2ErrorThreshold {
			delete(o.h2Failures, routeKey)
			return o.shadowSwitchActionLocked(routeKey, now)
		}
		return transportActionMarkSuspect
	case transportFailureH2RefusedStream, transportFailureH2Connection, transportFailureUnexpectedEOF, transportFailureNetwork:
		return transportActionReplaceConnection
	default:
		return transportActionNone
	}
}

func (o *transportShadowObserver) shadowSwitchActionLocked(routeKey string, now time.Time) transportAction {
	if holdUntil := o.routeHoldUntil[routeKey]; holdUntil.After(now) {
		return transportActionRouteHold
	}
	o.routeHoldUntil[routeKey] = now.Add(o.routeHold)
	return transportActionSwitchNode
}

func (o *transportShadowObserver) releaseSwitchHold(host, proxyRoute, selectedNode string) {
	if o == nil {
		return
	}
	routeKey := host + "\x00" + proxyRoute + "\x00" + normalizedSelectedNode(selectedNode)
	o.mu.Lock()
	delete(o.routeHoldUntil, routeKey)
	o.mu.Unlock()
}

func classifyTransportFailure(err error, phase transportFailurePhase) transportFailureClass {
	if err == nil {
		return transportFailureUnknown
	}
	if errors.Is(err, context.Canceled) {
		return transportFailureCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return transportFailureDeadline
	}

	var streamErr http2.StreamError
	if errors.As(err, &streamErr) {
		switch streamErr.Code {
		case http2.ErrCodeProtocol:
			return transportFailureH2Protocol
		case http2.ErrCodeInternal:
			return transportFailureH2Internal
		case http2.ErrCodeRefusedStream:
			return transportFailureH2RefusedStream
		}
	}
	if isClientConnEstablishmentError(err) {
		return transportFailureH2Establish
	}
	var connErr http2.ConnectionError
	if errors.As(err, &connErr) {
		return transportFailureH2Connection
	}
	var goAwayErr http2.GoAwayError
	if errors.As(err, &goAwayErr) {
		return transportFailureH2Connection
	}

	message := strings.ToUpper(err.Error())
	if strings.Contains(message, "PROTOCOL_ERROR") {
		return transportFailureH2Protocol
	}
	if strings.Contains(message, "INTERNAL_ERROR") {
		return transportFailureH2Internal
	}
	if strings.Contains(message, "REFUSED_STREAM") {
		return transportFailureH2RefusedStream
	}

	if phase == transportPhaseProxyConnect {
		if transportErrorIsTimeout(err) {
			return transportFailureProxyTimeout
		}
		if containsGatewayStatus(message) {
			return transportFailureProxyGateway
		}
		return transportFailureProxyConnect
	}
	if phase == transportPhaseTLSHandshake {
		return transportFailureTLS
	}
	if phase == transportPhaseH2Establish {
		return transportFailureH2Establish
	}

	if errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(message, "UNEXPECTED EOF") {
		return transportFailureUnexpectedEOF
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return transportFailureNetwork
	}
	if strings.Contains(message, "CONNECTION RESET BY PEER") ||
		strings.Contains(message, "CLIENT CONNECTION FORCE CLOSED") ||
		strings.Contains(message, "BROKEN PIPE") ||
		strings.Contains(message, "USE OF CLOSED NETWORK CONNECTION") ||
		strings.Contains(message, "SERVER SENT GOAWAY") ||
		strings.Contains(message, "CONNECTION ERROR") {
		return transportFailureNetwork
	}
	return transportFailureUnknown
}

func actualTransportAction(hasConnection bool, err error) transportAction {
	if !hasConnection {
		return transportActionDialFailed
	}
	if isClientConnEstablishmentError(err) {
		return transportActionRetireHostPool
	}
	switch connectionErrorDisposition(err) {
	case connErrorDrain:
		return transportActionDrainConnection
	case connErrorClose:
		return transportActionCloseConnection
	default:
		return transportActionKeepConnection
	}
}

func shadowReplayEligible(class transportFailureClass, committed, boundaryKnown bool, retryAttempt, retryBudget int) bool {
	if !boundaryKnown || committed || retryAttempt >= retryBudget {
		return false
	}
	switch class {
	case transportFailureProxyTimeout,
		transportFailureProxyGateway,
		transportFailureTLS,
		transportFailureH2Establish,
		transportFailureH2Protocol,
		transportFailureH2Internal,
		transportFailureH2RefusedStream,
		transportFailureH2Connection,
		transportFailureUnexpectedEOF,
		transportFailureNetwork:
		return true
	default:
		return false
	}
}

func transportErrorIsTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

func containsGatewayStatus(message string) bool {
	return strings.Contains(message, "CONNECT 502") ||
		strings.Contains(message, "CONNECT 504") ||
		strings.Contains(message, "502 BAD GATEWAY") ||
		strings.Contains(message, "504 GATEWAY TIMEOUT") ||
		strings.Contains(message, "STATUS CODE: 502") ||
		strings.Contains(message, "STATUS CODE: 504") ||
		strings.Contains(message, "STATUS=502") ||
		strings.Contains(message, "STATUS=504")
}

func normalizedProxyRoute(route string) string {
	route = strings.TrimSpace(route)
	if route == "" {
		return "direct"
	}
	return route
}

func normalizedSelectedNode(node string) string {
	node = strings.TrimSpace(node)
	if node == "" {
		return "unknown"
	}
	return node
}

func selectedNodeSource(node string) string {
	if strings.TrimSpace(node) == "" {
		return "controller_not_configured"
	}
	return "snapshot"
}

func logTransportFailure(ctx context.Context, observation transportFailureObservation) {
	fields := log.Fields{
		"shadow_mode":              true,
		"host":                     observation.Host,
		"proxy_route":              observation.ProxyRoute,
		"selected_node":            observation.SelectedNode,
		"selected_node_source":     observation.SelectedNodeSource,
		"connection_id":            observation.ConnectionID,
		"pool_generation":          observation.PoolGeneration,
		"failure_phase":            observation.Phase,
		"failure_class":            observation.Class,
		"actual_action":            observation.ActualAction,
		"shadow_action":            observation.ShadowAction,
		"payload_committed":        observation.PayloadCommitted,
		"payload_boundary_known":   observation.PayloadBoundaryKnown,
		"shadow_replay_eligible":   observation.ShadowReplayEligible,
		"retry_attempt":            observation.RetryAttempt,
		"retry_budget":             observation.RetryBudget,
		"failure_class_total":      observation.FailureClassTotal,
		"shadow_action_total":      observation.ShadowActionTotal,
		"shadow_node_switch_total": observation.ShadowSwitchTotal,
	}
	entry := LogWithRequestID(ctx).WithFields(fields)
	if observation.Class == transportFailureCanceled || observation.Class == transportFailureDeadline {
		entry.Debug("transport recovery shadow: failure observed")
		return
	}
	entry.Warn("transport recovery shadow: failure observed")
}

func logTransportRetry(ctx context.Context, observation transportRetryObservation) {
	fields := log.Fields{
		"shadow_mode":         true,
		"host":                observation.Host,
		"proxy_route":         observation.ProxyRoute,
		"selected_node":       observation.SelectedNode,
		"connection_id":       observation.ConnectionID,
		"pool_generation":     observation.PoolGeneration,
		"retry_attempt":       observation.RetryAttempt,
		"retry_budget":        observation.RetryBudget,
		"retry_outcome":       observation.Outcome,
		"retry_outcome_total": observation.RetryOutcomeTotal,
	}
	entry := LogWithRequestID(ctx).WithFields(fields)
	if observation.Outcome == transportRetrySucceeded {
		entry.Info("transport recovery shadow: existing retry finished")
		return
	}
	entry.Warn("transport recovery shadow: existing retry finished")
}
