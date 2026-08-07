package controller

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func ForceUnlockChannelQuotaProtection(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid channel id"})
		return
	}
	now := common.GetTimestamp()
	userID := c.GetInt("id")
	result, err := service.ForceUnlockCliproxyCPAQuotaGuard(channelID, userID, now)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	model.RecordLog(userID, model.LogTypeManage, fmt.Sprintf(
		"强制解除渠道 12 额度保护，自动失效时间：%d",
		result.Until,
	))
	common.ApiSuccess(c, result)
}

func CancelChannelQuotaProtectionForceUnlock(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid channel id"})
		return
	}
	now := common.GetTimestamp()
	userID := c.GetInt("id")
	result, err := service.CancelCliproxyCPAQuotaGuardForceUnlock(channelID, userID, now)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	model.RecordLog(userID, model.LogTypeManage, "恢复渠道 12 自动额度保护")
	common.ApiSuccess(c, result)
}
