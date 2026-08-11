package service

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProxyClientCacheCanonicalizesLegacyAliases(t *testing.T) {
	ResetProxyClientCache()
	t.Cleanup(ResetProxyClientCache)

	canonicalClient, err := GetHttpClientWithProxy("http://proxy.example:8080")
	if err != nil {
		t.Fatalf("GetHttpClientWithProxy(canonical) error = %v", err)
	}
	legacyClient, err := GetHttpClientWithProxy("http://proxy.example:8080/legacy?ignored=1")
	if err != nil {
		t.Fatalf("GetHttpClientWithProxy(legacy) error = %v", err)
	}
	if canonicalClient != legacyClient {
		t.Fatal("canonical and legacy proxy URLs should reuse the same client")
	}
}

func TestInvalidateProxyClientOnlyReplacesMatchingClient(t *testing.T) {
	ResetProxyClientCache()
	t.Cleanup(ResetProxyClientCache)

	first, err := GetHttpClientWithProxy("http://proxy-one.example:8080")
	if err != nil {
		t.Fatalf("GetHttpClientWithProxy(first) error = %v", err)
	}
	second, err := GetHttpClientWithProxy("http://proxy-two.example:8080")
	if err != nil {
		t.Fatalf("GetHttpClientWithProxy(second) error = %v", err)
	}

	InvalidateProxyClient("http://proxy-one.example:8080/")

	firstAfter, err := GetHttpClientWithProxy("http://proxy-one.example:8080")
	if err != nil {
		t.Fatalf("GetHttpClientWithProxy(first after invalidation) error = %v", err)
	}
	secondAfter, err := GetHttpClientWithProxy("http://proxy-two.example:8080")
	if err != nil {
		t.Fatalf("GetHttpClientWithProxy(second after invalidation) error = %v", err)
	}
	if firstAfter == first {
		t.Fatal("invalidated proxy client was reused")
	}
	if secondAfter != second {
		t.Fatal("unrelated proxy client should remain cached")
	}
}

func TestClientWithResponseHeaderTimeoutClonesAndCachesTransport(t *testing.T) {
	resetResponseHeaderTimeoutClients()
	t.Cleanup(resetResponseHeaderTimeoutClients)

	baseTransport := newRelayHTTPTransport()
	baseClient := newRelayHTTPClient(baseTransport)
	timeout := 60 * time.Second

	timedClient, err := clientWithResponseHeaderTimeout(baseClient, timeout)
	if err != nil {
		t.Fatalf("clientWithResponseHeaderTimeout() error = %v", err)
	}
	if timedClient == baseClient {
		t.Fatal("timed client must not mutate the shared base client")
	}
	timedTransport, ok := timedClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("timed transport type = %T, want *http.Transport", timedClient.Transport)
	}
	if timedTransport.ResponseHeaderTimeout != timeout {
		t.Fatalf("ResponseHeaderTimeout = %s, want %s", timedTransport.ResponseHeaderTimeout, timeout)
	}
	if baseTransport.ResponseHeaderTimeout != 0 {
		t.Fatalf("base ResponseHeaderTimeout = %s, want 0", baseTransport.ResponseHeaderTimeout)
	}

	cachedClient, err := clientWithResponseHeaderTimeout(baseClient, timeout)
	if err != nil {
		t.Fatalf("second clientWithResponseHeaderTimeout() error = %v", err)
	}
	if cachedClient != timedClient {
		t.Fatal("same base client and timeout should reuse the cached client")
	}

	untimedClient, err := clientWithResponseHeaderTimeout(baseClient, 0)
	if err != nil {
		t.Fatalf("untimed clientWithResponseHeaderTimeout() error = %v", err)
	}
	if untimedClient != baseClient {
		t.Fatal("zero timeout should return the shared base client")
	}
}

func TestClientWithResponseHeaderTimeoutStopsWaitingForHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resetResponseHeaderTimeoutClients()
	t.Cleanup(resetResponseHeaderTimeoutClients)
	baseClient := newRelayHTTPClient(newRelayHTTPTransport())
	timedClient, err := clientWithResponseHeaderTimeout(baseClient, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("clientWithResponseHeaderTimeout() error = %v", err)
	}

	started := time.Now()
	resp, err := timedClient.Get(server.URL)
	elapsed := time.Since(started)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("request unexpectedly received response headers")
	}
	var timeoutErr net.Error
	if !errors.As(err, &timeoutErr) || !timeoutErr.Timeout() {
		t.Fatalf("request error = %v, want timeout", err)
	}
	if elapsed >= 150*time.Millisecond {
		t.Fatalf("response header timeout elapsed = %s, want less than 150ms", elapsed)
	}
}

func TestClientWithResponseHeaderTimeoutDoesNotLimitBodyAfterHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(100 * time.Millisecond)
		_, _ = io.WriteString(w, "stream complete")
	}))
	defer server.Close()

	resetResponseHeaderTimeoutClients()
	t.Cleanup(resetResponseHeaderTimeoutClients)
	baseClient := newRelayHTTPClient(newRelayHTTPTransport())
	timedClient, err := clientWithResponseHeaderTimeout(baseClient, 25*time.Millisecond)
	if err != nil {
		t.Fatalf("clientWithResponseHeaderTimeout() error = %v", err)
	}

	resp, err := timedClient.Get(server.URL)
	if err != nil {
		t.Fatalf("request failed after headers were flushed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body after response header timeout window: %v", err)
	}
	if string(body) != "stream complete" {
		t.Fatalf("body = %q, want %q", body, "stream complete")
	}
}

func TestDoRequestWithPreResponseTimeoutStopsBlockedRequestUpload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	req, err := http.NewRequest(http.MethodPost, server.URL, reader)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := writer.Write([]byte("partial request body"))
		writeDone <- writeErr
	}()

	started := time.Now()
	resp, err := DoRequestWithPreResponseTimeout(http.DefaultClient, req, 40*time.Millisecond)
	elapsed := time.Since(started)
	_ = writer.Close()
	if writeErr := <-writeDone; writeErr != nil && !errors.Is(writeErr, io.ErrClosedPipe) {
		t.Fatalf("write request body prefix error = %v", writeErr)
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrPreResponseTimeout) {
		t.Fatalf("request error = %v, want ErrPreResponseTimeout", err)
	}
	if elapsed >= 200*time.Millisecond {
		t.Fatalf("pre-response timeout elapsed = %s, want less than 200ms", elapsed)
	}
}

func TestDoRequestWithPreResponseTimeoutDoesNotLimitBodyAfterHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(100 * time.Millisecond)
		_, _ = io.WriteString(w, "stream complete")
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	resp, err := DoRequestWithPreResponseTimeout(http.DefaultClient, req, 25*time.Millisecond)
	if err != nil {
		t.Fatalf("request failed after headers were flushed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body after pre-response timeout window: %v", err)
	}
	if string(body) != "stream complete" {
		t.Fatalf("body = %q, want %q", body, "stream complete")
	}
}

func TestDoRequestWithPreResponseTimeoutPreservesParentCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	parentCtx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(parentCtx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() error = %v", err)
	}
	time.AfterFunc(20*time.Millisecond, cancel)

	resp, err := DoRequestWithPreResponseTimeout(http.DefaultClient, req, 150*time.Millisecond)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("request error = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrPreResponseTimeout) {
		t.Fatalf("parent cancellation was mislabeled as pre-response timeout: %v", err)
	}
}

func TestDoRequestWithPreResponseTimeoutZeroKeepsOriginalRequest(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	var gotRequest *http.Request
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		gotRequest = request
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       http.NoBody,
			Header:     make(http.Header),
			Request:    request,
		}, nil
	})}

	resp, err := DoRequestWithPreResponseTimeout(client, req, 0)
	if err != nil {
		t.Fatalf("DoRequestWithPreResponseTimeout() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if gotRequest != req {
		t.Fatal("zero timeout should pass the original request to the client")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
