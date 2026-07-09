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
	requestID := strings.TrimSpace(c.Query("request_id"))
	audits := h.authManager.RecentDispatchAudits(limit)
	if requestID != "" {
		audits = h.authManager.RecentDispatchAudits(0)
		filtered := audits[:0]
		for _, audit := range audits {
			if strings.TrimSpace(audit.RequestID) == requestID {
				filtered = append(filtered, audit)
			}
		}
		audits = filtered
		if len(audits) > limit {
			audits = audits[:limit]
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"dispatches": audits,
	})
}
