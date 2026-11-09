package common

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/gin-gonic/gin"
)

// ConfigureTrustedProxies accepts forwarding headers only from explicitly
// configured peers. An empty list disables forwarding-header trust entirely.
func ConfigureTrustedProxies(server *gin.Engine, raw string) error {
	if err := server.SetTrustedProxies(nil); err != nil {
		return err
	}
	var proxies []string
	if strings.TrimSpace(raw) != "" {
		for _, value := range strings.Split(raw, ",") {
			value = strings.TrimSpace(value)
			if value == "" {
				return fmt.Errorf("TRUSTED_PROXIES contains an empty entry")
			}
			if strings.Contains(value, "/") {
				prefix, err := netip.ParsePrefix(value)
				if err != nil || prefix.Bits() == 0 {
					return fmt.Errorf("TRUSTED_PROXIES requires valid IPs or CIDRs narrower than /0")
				}
			} else if _, err := netip.ParseAddr(value); err != nil {
				return fmt.Errorf("TRUSTED_PROXIES requires IP addresses or CIDRs")
			}
			proxies = append(proxies, value)
		}
	}
	if err := server.SetTrustedProxies(proxies); err != nil {
		return fmt.Errorf("invalid TRUSTED_PROXIES configuration: %w", err)
	}
	// Nginx must overwrite this header with its verified peer address. Ignore
	// alternative client-IP headers so they cannot bypass the same trust chain.
	server.RemoteIPHeaders = []string{"X-Forwarded-For"}
	server.TrustedPlatform = ""
	return nil
}
