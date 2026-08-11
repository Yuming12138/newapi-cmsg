package service

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"golang.org/x/net/proxy"
)

var (
	httpClient              *http.Client
	ssrfProtectedHTTPClient *http.Client
	proxyClients            = proxyHTTPClientCache{
		clients: make(map[string]*http.Client),
		aliases: make(map[string]string),
	}
	responseHeaderTimeoutClients sync.Map
	legacyProxyURLWarnings       sync.Map
)

type proxyHTTPClientCache struct {
	mutex   sync.RWMutex
	clients map[string]*http.Client
	aliases map[string]string
}

type responseHeaderTimeoutClientKey struct {
	baseClient *http.Client
	timeout    time.Duration
}

// ErrPreResponseTimeout identifies a timeout while sending the upstream
// request or waiting for its response headers. It intentionally unwraps to
// context.DeadlineExceeded so existing timeout classification keeps working.
var ErrPreResponseTimeout = fmt.Errorf("upstream pre-response timeout: %w", context.DeadlineExceeded)

type cancelOnCloseReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (body *cancelOnCloseReadCloser) release() {
	body.once.Do(body.cancel)
}

func (body *cancelOnCloseReadCloser) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	if err != nil {
		body.release()
	}
	return n, err
}

func (body *cancelOnCloseReadCloser) Close() error {
	err := body.ReadCloser.Close()
	body.release()
	return err
}

type proxyURLConfig struct {
	parsedURL *url.URL
	cacheKey  string
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	urlStr := req.URL.String()
	if err := validateURLWithCurrentFetchSetting(urlStr, true); err != nil {
		return fmt.Errorf("redirect to %s blocked: %v", urlStr, err)
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	return nil
}

func checkProtectedFetchRedirect(req *http.Request, via []*http.Request) error {
	urlStr := req.URL.String()
	if err := ValidateSSRFProtectedFetchURL(urlStr); err != nil {
		return fmt.Errorf("redirect to %s blocked: %v", urlStr, err)
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	return nil
}

func validateURLWithCurrentFetchSetting(urlStr string, applyDomainIPFilter bool) error {
	fetchSetting := system_setting.GetFetchSetting()
	return common.ValidateURLWithFetchSetting(urlStr, fetchSetting.EnableSSRFProtection, fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, applyDomainIPFilter && fetchSetting.ApplyIPFilterForDomain)
}

func ValidateSSRFProtectedFetchURL(urlStr string) error {
	return validateURLWithCurrentFetchSetting(urlStr, true)
}

func newRelayHTTPTransport() *http.Transport {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          common.RelayMaxIdleConns,
		MaxIdleConnsPerHost:   common.RelayMaxIdleConnsPerHost,
		IdleConnTimeout:       time.Duration(common.RelayIdleConnTimeout) * time.Second,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if common.TLSInsecureSkipVerify {
		transport.TLSClientConfig = common.InsecureTLSConfig
	}
	return transport
}

func newRelayHTTPClient(transport *http.Transport) *http.Client {
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: checkRedirect,
	}
	if common.RelayTimeout != 0 {
		client.Timeout = time.Duration(common.RelayTimeout) * time.Second
	}
	return client
}

func InitHttpClient() {
	resetResponseHeaderTimeoutClients()
	transport := newRelayHTTPTransport()
	transport.Proxy = http.ProxyFromEnvironment
	httpClient = newRelayHTTPClient(transport)
	ssrfProtectedHTTPClient = newProtectedFetchHTTPClient()
}

// GetHttpClient returns the general outbound client used by relay/provider
// integrations. Do not attach the SSRF-protected dialer here: provider base URLs
// are root/operator-managed deployment targets, not arbitrary user-controlled
// input, and may legitimately point at private networks, private-link endpoints,
// self-hosted services, or local proxies. Code paths that fetch arbitrary
// user-controlled URLs must use GetSSRFProtectedHTTPClient or
// ValidateSSRFProtectedFetchURL instead.
func GetHttpClient() *http.Client {
	return httpClient
}

// GetSSRFProtectedHTTPClient returns the client with dial-time SSRF validation.
// InitHttpClient initializes it once and runtime callers only read it.
func GetSSRFProtectedHTTPClient() *http.Client {
	if fetchSetting := system_setting.GetFetchSetting(); fetchSetting != nil && !fetchSetting.EnableSSRFProtection {
		return GetHttpClient()
	}
	return ssrfProtectedHTTPClient
}

func newProxyURLConfig(parsedURL *url.URL) *proxyURLConfig {
	return &proxyURLConfig{
		parsedURL: parsedURL,
		cacheKey:  parsedURL.String(),
	}
}

func warnLegacyProxyURLOnce(config *proxyURLConfig) {
	if _, loaded := legacyProxyURLWarnings.LoadOrStore(config.cacheKey, struct{}{}); loaded {
		return
	}
	logger.LogWarn(
		context.Background(),
		fmt.Sprintf(
			"legacy proxy URL suffix ignored at runtime: scheme=%s host=%s; update the channel proxy setting",
			config.parsedURL.Scheme,
			config.parsedURL.Host,
		),
	)
}

// NormalizeProxyURL validates a proxy URL using runtime-compatible rules and returns its canonical cache key.
func NormalizeProxyURL(rawProxyURL string) (string, error) {
	parsedURL, legacySuffixStripped, err := common.ParseProxyURLRuntime(rawProxyURL)
	if err != nil {
		return "", err
	}
	if parsedURL == nil {
		return "", nil
	}
	config := newProxyURLConfig(parsedURL)
	if legacySuffixStripped {
		warnLegacyProxyURLOnce(config)
	}
	return config.cacheKey, nil
}

// ValidateProxyURL validates a channel proxy URL without connecting to it.
func ValidateProxyURL(rawProxyURL string) error {
	_, err := common.ParseProxyURLStrict(rawProxyURL)
	return err
}

func (cache *proxyHTTPClientCache) get(rawCacheKey string) (*http.Client, bool) {
	cache.mutex.RLock()
	defer cache.mutex.RUnlock()
	cacheKey := rawCacheKey
	if canonicalKey, ok := cache.aliases[rawCacheKey]; ok {
		cacheKey = canonicalKey
	}
	client, ok := cache.clients[cacheKey]
	return client, ok
}

func (cache *proxyHTTPClientCache) getOrCreate(rawCacheKey string, config *proxyURLConfig) (*http.Client, error) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	if client, ok := cache.clients[config.cacheKey]; ok {
		cache.aliases[rawCacheKey] = config.cacheKey
		return client, nil
	}

	client, err := newProxyHTTPClient(config.parsedURL)
	if err != nil {
		return nil, err
	}
	cache.clients[config.cacheKey] = client
	cache.aliases[rawCacheKey] = config.cacheKey
	return client, nil
}

func (cache *proxyHTTPClientCache) remove(cacheKey string) *http.Client {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	client := cache.clients[cacheKey]
	delete(cache.clients, cacheKey)
	for alias, canonicalKey := range cache.aliases {
		if canonicalKey == cacheKey {
			delete(cache.aliases, alias)
		}
	}
	return client
}

func (cache *proxyHTTPClientCache) reset() map[string]*http.Client {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	oldClients := cache.clients
	cache.clients = make(map[string]*http.Client)
	cache.aliases = make(map[string]string)
	return oldClients
}

func newProxyHTTPClient(proxyURL *url.URL) (*http.Client, error) {
	transport := newRelayHTTPTransport()

	switch proxyURL.Scheme {
	case "http", "https":
		transport.Proxy = http.ProxyURL(proxyURL)
	case "socks5", "socks5h":
		transport.Proxy = nil
		forwardDialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		dialer, err := proxy.FromURL(proxyURL, forwardDialer)
		if err != nil {
			return nil, err
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("SOCKS proxy dialer does not support context cancellation")
		}
		transport.DialContext = contextDialer.DialContext
	default:
		return nil, fmt.Errorf("unsupported proxy scheme")
	}

	return newRelayHTTPClient(transport), nil
}

// GetHttpClientWithProxy returns the default client or a cached proxy-enabled client.
func GetHttpClientWithProxy(rawProxyURL string) (*http.Client, error) {
	trimmedProxyURL := strings.TrimSpace(rawProxyURL)
	if trimmedProxyURL == "" {
		if client := GetHttpClient(); client != nil {
			return client, nil
		}
		return http.DefaultClient, nil
	}
	if client, ok := proxyClients.get(trimmedProxyURL); ok {
		return client, nil
	}

	parsedURL, legacySuffixStripped, err := common.ParseProxyURLRuntime(trimmedProxyURL)
	if err != nil {
		return nil, err
	}
	config := newProxyURLConfig(parsedURL)
	if legacySuffixStripped {
		warnLegacyProxyURLOnce(config)
	}
	return proxyClients.getOrCreate(trimmedProxyURL, config)
}

func clientWithResponseHeaderTimeout(baseClient *http.Client, timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		return baseClient, nil
	}
	if baseClient == nil {
		baseClient = http.DefaultClient
	}
	key := responseHeaderTimeoutClientKey{baseClient: baseClient, timeout: timeout}
	if cached, ok := responseHeaderTimeoutClients.Load(key); ok {
		return cached.(*http.Client), nil
	}

	baseTransport := baseClient.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	transport, ok := baseTransport.(*http.Transport)
	if !ok || transport == nil {
		return nil, fmt.Errorf("response header timeout requires *http.Transport, got %T", baseTransport)
	}
	clonedTransport := transport.Clone()
	clonedTransport.ResponseHeaderTimeout = timeout
	clonedClient := *baseClient
	clonedClient.Transport = clonedTransport

	actual, loaded := responseHeaderTimeoutClients.LoadOrStore(key, &clonedClient)
	if loaded {
		clonedTransport.CloseIdleConnections()
		return actual.(*http.Client), nil
	}
	return &clonedClient, nil
}

// GetHttpClientWithProxyAndResponseHeaderTimeout returns a cached relay client
// whose timeout applies only while waiting for upstream response headers.
func GetHttpClientWithProxyAndResponseHeaderTimeout(rawProxyURL string, timeout time.Duration) (*http.Client, error) {
	baseClient, err := GetHttpClientWithProxy(rawProxyURL)
	if err != nil {
		return nil, err
	}
	return clientWithResponseHeaderTimeout(baseClient, timeout)
}

// DoRequestWithPreResponseTimeout limits only the phase before client.Do
// returns: connection setup, request-body upload, and response-header wait.
// Once headers arrive, the timer is stopped and the response body (including
// an SSE stream) may continue until its normal completion or parent cancel.
func DoRequestWithPreResponseTimeout(client *http.Client, req *http.Request, timeout time.Duration) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if timeout <= 0 || req == nil {
		return client.Do(req)
	}

	parentCtx := req.Context()
	requestCtx, cancel := context.WithCancel(parentCtx)
	timedOut := atomic.Bool{}
	timerDone := make(chan struct{})
	timer := time.AfterFunc(timeout, func() {
		timedOut.Store(true)
		cancel()
		if req.Body != nil {
			_ = req.Body.Close()
		}
		close(timerDone)
	})

	resp, err := client.Do(req.Clone(requestCtx))
	if !timer.Stop() {
		<-timerDone
	}

	if timedOut.Load() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		cancel()
		if parentCtx.Err() == nil {
			return nil, ErrPreResponseTimeout
		}
		if err != nil {
			return nil, err
		}
		return nil, parentCtx.Err()
	}
	if err != nil {
		cancel()
		return resp, err
	}
	if resp == nil || resp.Body == nil {
		cancel()
		return resp, nil
	}

	resp.Body = &cancelOnCloseReadCloser{
		ReadCloser: resp.Body,
		cancel:     cancel,
	}
	return resp, nil
}

func resetResponseHeaderTimeoutClients() {
	responseHeaderTimeoutClients.Range(func(key, value any) bool {
		if client, ok := value.(*http.Client); ok && client != nil {
			client.CloseIdleConnections()
		}
		responseHeaderTimeoutClients.Delete(key)
		return true
	})
}

// InvalidateProxyClient removes one proxy client and closes its idle connections.
func InvalidateProxyClient(rawProxyURL string) {
	parsedURL, legacySuffixStripped, err := common.ParseProxyURLRuntime(rawProxyURL)
	if err != nil || parsedURL == nil {
		return
	}
	config := newProxyURLConfig(parsedURL)
	if legacySuffixStripped {
		warnLegacyProxyURLOnce(config)
	}
	if client := proxyClients.remove(config.cacheKey); client != nil {
		client.CloseIdleConnections()
		resetResponseHeaderTimeoutClients()
	}
}

// ResetProxyClientCache clears all cached proxy clients.
func ResetProxyClientCache() {
	resetResponseHeaderTimeoutClients()
	for _, client := range proxyClients.reset() {
		client.CloseIdleConnections()
	}
}

// NewProxyHttpClient is kept for compatibility.
// Deprecated: use GetHttpClientWithProxy.
func NewProxyHttpClient(proxyURL string) (*http.Client, error) {
	return GetHttpClientWithProxy(proxyURL)
}
