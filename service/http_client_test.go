package service

import "testing"

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
