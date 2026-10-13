package controller

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func GetCodexRadarOverview(c *gin.Context) {
	getCodexRadarOverview(c, service.GetCodexRadarOverview)
}

func getCodexRadarOverview(c *gin.Context, getOverview func(context.Context) (service.CodexRadarOverview, error)) {
	overview, err := getOverview(c.Request.Context())
	if err != nil {
		common.SysLog("failed to fetch Codex Radar overview: " + err.Error())
		delay := service.CodexRadarRetryDelay(err)
		c.Header("Retry-After", strconv.FormatInt(int64((delay+time.Second-1)/time.Second), 10))
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"message": "Codex Radar data is temporarily unavailable",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    overview,
	})
}
