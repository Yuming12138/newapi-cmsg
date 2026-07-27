package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func GetCodexRadarOverview(c *gin.Context) {
	overview, err := service.GetCodexRadarOverview(c.Request.Context())
	if err != nil {
		common.SysLog("failed to fetch Codex Radar overview: " + err.Error())
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
