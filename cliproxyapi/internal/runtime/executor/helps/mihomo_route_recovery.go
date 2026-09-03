package helps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	log "github.com/sirupsen/logrus"
)

const (
	defaultRouteRecoveryControllerTimeout = 5 * time.Second
	defaultRouteRecoveryNodeCooldown      = 15 * time.Minute
	defaultRouteRecoveryRepeatCooldown    = 30 * time.Minute
	defaultRouteRecoveryMaxReplays        = 1
	defaultRouteRecoveryProbeURL          = "https://chatgpt.com/cdn-cgi/trace"
	defaultRouteRecoveryProbeTimeout      = 4 * time.Second
)

type proxyRouteRecoverySettings struct {
	enabled                 bool
	controllerURL           string
	controllerSecretFile    string
	group                   string
	hosts                   map[string]struct{}
	probeURL                string
	probeTimeout            time.Duration
	h2ErrorWindow           time.Duration
	h2ErrorThreshold        int
	nodeCooldown            time.Duration
	repeatedFailureCooldown time.Duration
	routeHold               time.Duration
	maxReplays              int
}

func normalizeProxyRouteRecoverySettings(raw config.ProxyRouteRecoveryConfig, proxyURL string) proxyRouteRecoverySettings {
	probeURL := strings.TrimSpace(raw.ProbeURL)
	if probeURL == "" && len(raw.Hosts) == 1 && strings.EqualFold(strings.TrimSpace(raw.Hosts[0]), "chatgpt.com") {
		probeURL = defaultRouteRecoveryProbeURL
	}
	probeTimeout := time.Duration(raw.ProbeTimeoutMs) * time.Millisecond
	if probeTimeout <= 0 || probeTimeout >= defaultRouteRecoveryControllerTimeout {
		probeTimeout = defaultRouteRecoveryProbeTimeout
	}
	settings := proxyRouteRecoverySettings{
		controllerURL:           strings.TrimRight(strings.TrimSpace(raw.ControllerURL), "/"),
		controllerSecretFile:    strings.TrimSpace(raw.ControllerSecretFile),
		group:                   strings.TrimSpace(raw.Group),
		hosts:                   make(map[string]struct{}),
		probeURL:                probeURL,
		probeTimeout:            probeTimeout,
		h2ErrorWindow:           parsePositiveDuration(raw.H2ErrorWindow, transportShadowH2ErrorWindow),
		h2ErrorThreshold:        raw.H2ErrorThreshold,
		nodeCooldown:            parsePositiveDuration(raw.NodeCooldown, defaultRouteRecoveryNodeCooldown),
		repeatedFailureCooldown: parsePositiveDuration(raw.RepeatedFailureCooldown, defaultRouteRecoveryRepeatCooldown),
		routeHold:               parsePositiveDuration(raw.RouteHold, transportShadowRouteHold),
		maxReplays:              raw.MaxReplays,
	}
	if settings.h2ErrorThreshold <= 0 {
		settings.h2ErrorThreshold = 2
	}
	if settings.maxReplays <= 0 {
		settings.maxReplays = defaultRouteRecoveryMaxReplays
	}
	if settings.maxReplays > defaultRouteRecoveryMaxReplays {
		settings.maxReplays = defaultRouteRecoveryMaxReplays
	}
	for _, host := range raw.Hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			settings.hosts[host] = struct{}{}
		}
	}
	settings.enabled = raw.Enabled &&
		settings.controllerURL != "" &&
		settings.controllerSecretFile != "" &&
		settings.group != "" &&
		len(settings.hosts) > 0 &&
		proxyAndControllerShareHost(proxyURL, settings.controllerURL)
	return settings
}

func parsePositiveDuration(raw string, fallback time.Duration) time.Duration {
	parsed, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func proxyAndControllerShareHost(proxyURL, controllerURL string) bool {
	proxyParsed, errProxy := url.Parse(strings.TrimSpace(proxyURL))
	controllerParsed, errController := url.Parse(strings.TrimSpace(controllerURL))
	if errProxy != nil || errController != nil {
		return false
	}
	proxyHost := proxyParsed.Hostname()
	controllerHost := controllerParsed.Hostname()
	return proxyHost != "" && controllerHost != "" && strings.EqualFold(proxyHost, controllerHost)
}

func (s proxyRouteRecoverySettings) allowsHost(host string) bool {
	if !s.enabled {
		return false
	}
	_, ok := s.hosts[strings.ToLower(strings.TrimSpace(host))]
	return ok
}

func proxyRouteRecoveryCacheKey(raw config.ProxyRouteRecoveryConfig) string {
	hosts := append([]string(nil), raw.Hosts...)
	for i := range hosts {
		hosts[i] = strings.ToLower(strings.TrimSpace(hosts[i]))
	}
	sort.Strings(hosts)
	return strings.Join([]string{
		strconv.FormatBool(raw.Enabled),
		strings.TrimSpace(raw.ControllerURL),
		strings.TrimSpace(raw.ControllerSecretFile),
		strings.TrimSpace(raw.Group),
		strings.Join(hosts, ","),
		strings.TrimSpace(raw.ProbeURL),
		strconv.Itoa(raw.ProbeTimeoutMs),
		strings.TrimSpace(raw.H2ErrorWindow),
		strconv.Itoa(raw.H2ErrorThreshold),
		strings.TrimSpace(raw.NodeCooldown),
		strings.TrimSpace(raw.RepeatedFailureCooldown),
		strings.TrimSpace(raw.RouteHold),
		strconv.Itoa(raw.MaxReplays),
	}, "\x00")
}

type mihomoProxyGroup struct {
	Name string   `json:"name"`
	Now  string   `json:"now"`
	All  []string `json:"all"`
}

type mihomoProxyState struct {
	Alive *bool `json:"alive"`
}

type mihomoProxyDelay struct {
	Delay int `json:"delay"`
}

type proxyRouteController interface {
	Group(context.Context) (mihomoProxyGroup, error)
	NodeAlive(context.Context, string) (bool, error)
	NodeDelay(context.Context, string, string, time.Duration) (time.Duration, error)
	SelectNode(context.Context, string) error
}

type mihomoRouteController struct {
	baseURL    string
	secretFile string
	group      string
	client     *http.Client
}

func newMihomoRouteController(settings proxyRouteRecoverySettings) *mihomoRouteController {
	return &mihomoRouteController{
		baseURL:    settings.controllerURL,
		secretFile: settings.controllerSecretFile,
		group:      settings.group,
		client: &http.Client{
			Timeout: defaultRouteRecoveryControllerTimeout,
		},
	}
}

func (c *mihomoRouteController) Group(ctx context.Context) (mihomoProxyGroup, error) {
	var group mihomoProxyGroup
	if err := c.doJSON(ctx, http.MethodGet, c.proxyEndpoint(c.group), nil, &group); err != nil {
		return group, fmt.Errorf("mihomo get group %q: %w", c.group, err)
	}
	if strings.TrimSpace(group.Now) == "" || len(group.All) == 0 {
		return group, fmt.Errorf("mihomo group %q returned no selected node or candidates", c.group)
	}
	return group, nil
}

func (c *mihomoRouteController) NodeAlive(ctx context.Context, node string) (bool, error) {
	var state mihomoProxyState
	if err := c.doJSON(ctx, http.MethodGet, c.proxyEndpoint(node), nil, &state); err != nil {
		return false, fmt.Errorf("mihomo get node %q: %w", node, err)
	}
	if state.Alive == nil {
		return true, nil
	}
	return *state.Alive, nil
}

func (c *mihomoRouteController) NodeDelay(ctx context.Context, node, probeURL string, timeout time.Duration) (time.Duration, error) {
	probeURL = strings.TrimSpace(probeURL)
	if probeURL == "" {
		return 0, errors.New("Mihomo probe URL is empty")
	}
	probeTimeoutMs := int(timeout / time.Millisecond)
	if probeTimeoutMs <= 0 {
		probeTimeoutMs = int(defaultRouteRecoveryProbeTimeout / time.Millisecond)
	}
	query := url.Values{}
	query.Set("url", probeURL)
	query.Set("timeout", strconv.Itoa(probeTimeoutMs))
	endpoint := c.proxyEndpoint(node) + "/delay?" + query.Encode()
	var delay mihomoProxyDelay
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &delay); err != nil {
		return 0, fmt.Errorf("Mihomo delay probe for node %q: %w", node, err)
	}
	if delay.Delay <= 0 {
		return 0, fmt.Errorf("Mihomo delay probe for node %q returned %d ms", node, delay.Delay)
	}
	return time.Duration(delay.Delay) * time.Millisecond, nil
}

func (c *mihomoRouteController) SelectNode(ctx context.Context, node string) error {
	body, errMarshal := json.Marshal(map[string]string{"name": node})
	if errMarshal != nil {
		return fmt.Errorf("marshal Mihomo node selection: %w", errMarshal)
	}
	if err := c.doJSON(ctx, http.MethodPut, c.proxyEndpoint(c.group), body, nil); err != nil {
		return fmt.Errorf("mihomo select node %q for group %q: %w", node, c.group, err)
	}
	return nil
}

func (c *mihomoRouteController) proxyEndpoint(name string) string {
	return c.baseURL + "/proxies/" + url.PathEscape(name)
}

func (c *mihomoRouteController) doJSON(ctx context.Context, method, endpoint string, body []byte, out any) error {
	secretBytes, errRead := os.ReadFile(c.secretFile)
	if errRead != nil {
		return fmt.Errorf("read controller secret: %w", errRead)
	}
	secret := strings.TrimSpace(string(secretBytes))
	if secret == "" {
		return errors.New("controller secret is empty")
	}

	req, errRequest := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if errRequest != nil {
		return fmt.Errorf("build controller request: %w", errRequest)
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, errDo := c.client.Do(req)
	if errDo != nil {
		return errDo
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Debugf("transport route recovery: close controller response: %v", errClose)
		}
	}()
	responseBody, errReadBody := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if errReadBody != nil {
		return fmt.Errorf("read controller response: %w", errReadBody)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("controller returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if out == nil || len(bytes.TrimSpace(responseBody)) == 0 {
		return nil
	}
	if errUnmarshal := json.Unmarshal(responseBody, out); errUnmarshal != nil {
		return fmt.Errorf("decode controller response: %w", errUnmarshal)
	}
	return nil
}

type proxyRouteRecoveryCoordinator struct {
	mu   sync.Mutex
	cond *sync.Cond

	settings     proxyRouteRecoverySettings
	controller   proxyRouteController
	now          func() time.Time
	switching    bool
	cooldown     map[string]time.Time
	failureCount map[string]int
	lastResult   routeRecoveryResult

	onNodeObserved func(host, node string)
	onSwitched     func(host, node string) uint64
}

type routeRecoveryResult struct {
	Attempted      bool
	Switched       bool
	Joined         bool
	PreviousNode   string
	SelectedNode   string
	PoolGeneration uint64
	Err            error
}

func newProxyRouteRecoveryCoordinator(settings proxyRouteRecoverySettings, controller proxyRouteController) *proxyRouteRecoveryCoordinator {
	coordinator := &proxyRouteRecoveryCoordinator{
		settings:     settings,
		controller:   controller,
		now:          time.Now,
		cooldown:     make(map[string]time.Time),
		failureCount: make(map[string]int),
	}
	coordinator.cond = sync.NewCond(&coordinator.mu)
	return coordinator
}

func (c *proxyRouteRecoveryCoordinator) waitForStableRoute(host string) {
	if c == nil || !c.settings.allowsHost(host) {
		return
	}
	c.mu.Lock()
	for c.switching {
		c.cond.Wait()
	}
	c.mu.Unlock()
}

func (c *proxyRouteRecoveryCoordinator) recover(host string) routeRecoveryResult {
	if c == nil || !c.settings.allowsHost(host) || c.controller == nil {
		return routeRecoveryResult{}
	}

	c.mu.Lock()
	if c.switching {
		for c.switching {
			c.cond.Wait()
		}
		result := c.lastResult
		result.Joined = true
		c.mu.Unlock()
		return result
	}
	c.switching = true
	c.mu.Unlock()

	result := c.performRecovery(host)
	c.mu.Lock()
	c.lastResult = result
	c.switching = false
	c.cond.Broadcast()
	c.mu.Unlock()
	return result
}

func (c *proxyRouteRecoveryCoordinator) performRecovery(host string) routeRecoveryResult {
	result := routeRecoveryResult{Attempted: true}
	ctx, cancel := context.WithTimeout(context.Background(), defaultRouteRecoveryControllerTimeout)
	defer cancel()

	group, errGroup := c.controller.Group(ctx)
	if errGroup != nil {
		result.Err = errGroup
		c.logFailure(host, result)
		return result
	}
	current := strings.TrimSpace(group.Now)
	result.PreviousNode = current
	c.recordSelectedNode(host, current)

	next, errCandidate := c.selectCandidate(ctx, group, current)
	if errCandidate != nil {
		result.Err = errCandidate
		result.SelectedNode = current
		c.logFailure(host, result)
		return result
	}
	if errSelect := c.controller.SelectNode(ctx, next); errSelect != nil {
		result.Err = errSelect
		result.SelectedNode = current
		c.logFailure(host, result)
		return result
	}

	now := c.now()
	c.mu.Lock()
	c.failureCount[current]++
	cooldown := c.settings.nodeCooldown
	if c.failureCount[current] > 1 {
		cooldown = c.settings.repeatedFailureCooldown
	}
	c.cooldown[current] = now.Add(cooldown)
	c.mu.Unlock()

	result.Switched = true
	result.SelectedNode = next
	if c.onNodeObserved != nil {
		c.onNodeObserved(host, next)
	}
	if c.onSwitched != nil {
		result.PoolGeneration = c.onSwitched(host, next)
	}
	log.WithFields(log.Fields{
		"host":            host,
		"group":           c.settings.group,
		"previous_node":   current,
		"selected_node":   next,
		"node_cooldown":   cooldown,
		"pool_generation": result.PoolGeneration,
	}).Warn("transport route recovery: Mihomo node switched")
	return result
}

func (c *proxyRouteRecoveryCoordinator) selectCandidate(ctx context.Context, group mihomoProxyGroup, current string) (string, error) {
	candidates := orderedRouteCandidates(group.All, current)
	now := c.now()
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || candidate == current {
			continue
		}
		c.mu.Lock()
		cooldownUntil := c.cooldown[candidate]
		c.mu.Unlock()
		if cooldownUntil.After(now) {
			continue
		}
		alive, errAlive := c.controller.NodeAlive(ctx, candidate)
		if errAlive != nil {
			log.WithError(errAlive).WithField("candidate_node", candidate).Debug("transport route recovery: candidate probe failed")
			continue
		}
		if alive {
			if c.settings.probeURL != "" {
				delay, errDelay := c.controller.NodeDelay(ctx, candidate, c.settings.probeURL, c.settings.probeTimeout)
				if errDelay != nil {
					log.WithError(errDelay).WithField("candidate_node", candidate).Debug("transport route recovery: upstream probe failed")
					continue
				}
				log.WithFields(log.Fields{
					"candidate_node": candidate,
					"probe_url":      c.settings.probeURL,
					"probe_delay":    delay,
				}).Debug("transport route recovery: upstream probe passed")
			}
			return candidate, nil
		}
	}
	return "", fmt.Errorf("Mihomo group %q has no alive candidate outside cooldown", c.settings.group)
}

func orderedRouteCandidates(nodes []string, current string) []string {
	if len(nodes) == 0 {
		return nil
	}
	start := -1
	for i, node := range nodes {
		if strings.TrimSpace(node) == current {
			start = i
			break
		}
	}
	ordered := make([]string, 0, len(nodes))
	for offset := 1; offset <= len(nodes); offset++ {
		ordered = append(ordered, nodes[(start+offset+len(nodes))%len(nodes)])
	}
	return ordered
}

func (c *proxyRouteRecoveryCoordinator) recordSelectedNode(host, node string) {
	if node == "" {
		return
	}
	if c.onNodeObserved != nil {
		c.onNodeObserved(host, node)
	}
}

func (c *proxyRouteRecoveryCoordinator) logFailure(host string, result routeRecoveryResult) {
	log.WithError(result.Err).WithFields(log.Fields{
		"host":          host,
		"group":         c.settings.group,
		"selected_node": result.PreviousNode,
	}).Warn("transport route recovery: controller recovery failed; keeping current route")
}
