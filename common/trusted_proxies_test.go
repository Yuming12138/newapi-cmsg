package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestConfigureTrustedProxiesClientIP(t *testing.T) {
	for _, tt := range []struct {
		name, proxies, peer, forwarded, realIP, want string
	}{
		{"empty configuration", "", "198.51.100.10:1234", "10.0.0.1", "10.0.0.2", "198.51.100.10"},
		{"untrusted direct peer", "172.18.0.1/32", "198.51.100.10:1234", "10.0.0.1", "10.0.0.2", "198.51.100.10"},
		{"trusted bridge", "172.18.0.1/32", "172.18.0.1:1234", "198.51.100.10", "10.0.0.2", "198.51.100.10"},
		{"spoofed prefix", "172.18.0.1/32", "172.18.0.1:1234", "10.0.0.1, 198.51.100.10", "10.0.0.2", "198.51.100.10"},
		{"ignore alternative header", "172.18.0.1/32", "172.18.0.1:1234", "", "10.0.0.2", "172.18.0.1"},
		{"invalid forwarded header", "172.18.0.1/32", "172.18.0.1:1234", "invalid", "10.0.0.2", "172.18.0.1"},
		{"IPv6 proxy", "127.0.0.1, ::1/128", "[::1]:1234", "2001:db8::1", "10.0.0.2", "2001:db8::1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := gin.New()
			require.NoError(t, ConfigureTrustedProxies(server, tt.proxies))
			server.GET("/", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.peer
			req.Header.Set("X-Forwarded-For", tt.forwarded)
			req.Header.Set("X-Real-IP", tt.realIP)
			req.Header.Set("CF-Connecting-IP", "10.0.0.3")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, req)
			require.Equal(t, tt.want, response.Body.String())
		})
	}
}

func TestConfigureTrustedProxiesRejectsInvalidConfiguration(t *testing.T) {
	for _, raw := range []string{"*", "0.0.0.0/0", "::/0", "localhost", "172.18.0.1,", "172.18.0.1,,127.0.0.1", "172.18.0.1/33"} {
		t.Run(raw, func(t *testing.T) {
			server := gin.New()
			require.Error(t, ConfigureTrustedProxies(server, raw))
			server.GET("/", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "198.51.100.10:1234"
			req.Header.Set("X-Forwarded-For", "10.0.0.1")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, req)
			require.Equal(t, "198.51.100.10", response.Body.String())
		})
	}
}
