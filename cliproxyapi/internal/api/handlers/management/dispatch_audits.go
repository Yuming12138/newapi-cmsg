package management

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

const defaultDispatchAuditLimit = 20

// GetDispatchAudits returns recent request-scoped scheduler diagnostics.
func (h *Handler) GetDispatchAudits(c *gin.Context) {
	if h == nil || h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}
	limit := defaultDispatchAuditLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"dispatches": h.authManager.RecentDispatchAudits(limit),
	})
}
