package controller

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"github.com/gin-gonic/gin"
)

const (
	defaultASXSAutoDailyResetControlPath = "/data/ops-control/asxs-auto-daily-reset.json"
	asxsAutoDailyResetControlSiteID      = "aliyun"
)

type asxsAutoDailyResetControl struct {
	SchemaVersion int    `json:"schema_version"`
	SiteID        string `json:"site_id"`
	Enabled       bool   `json:"enabled"`
	UpdatedAt     int64  `json:"updated_at"`
}

type updateASXSAutoDailyResetControlRequest struct {
	Enabled *bool `json:"enabled"`
}

func asxsAutoDailyResetControlPath() string {
	if path := strings.TrimSpace(os.Getenv("ASXS_AUTO_DAILY_RESET_CONTROL_PATH")); path != "" {
		return path
	}
	return defaultASXSAutoDailyResetControlPath
}

func readASXSAutoDailyResetControl() (asxsAutoDailyResetControl, bool, error) {
	var control asxsAutoDailyResetControl
	raw, err := os.ReadFile(asxsAutoDailyResetControlPath())
	if errors.Is(err, os.ErrNotExist) {
		return control, false, nil
	}
	if err != nil {
		return control, false, err
	}
	if err := common.Unmarshal(raw, &control); err != nil {
		return control, false, err
	}
	if control.SchemaVersion != 1 || control.SiteID != asxsAutoDailyResetControlSiteID {
		return control, false, fmt.Errorf("invalid ASXS auto reset control metadata")
	}
	return control, true, nil
}

func writeASXSAutoDailyResetControl(control asxsAutoDailyResetControl) error {
	path := asxsAutoDailyResetControlPath()
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	raw, err := common.Marshal(control)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	temporary, err := os.CreateTemp(directory, ".asxs-auto-daily-reset-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func GetASXSAutoDailyResetControl(c *gin.Context) {
	control, configured, err := readASXSAutoDailyResetControl()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "读取 ASXS 自动重置开关失败",
		})
		return
	}
	if !configured {
		control = asxsAutoDailyResetControl{
			SchemaVersion: 1,
			SiteID:        asxsAutoDailyResetControlSiteID,
			Enabled:       false,
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"configured": configured,
			"site_id":    control.SiteID,
			"enabled":    control.Enabled,
			"updated_at": control.UpdatedAt,
		},
	})
}

func UpdateASXSAutoDailyResetControl(c *gin.Context) {
	var request updateASXSAutoDailyResetControlRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "enabled 必须为布尔值",
		})
		return
	}
	control := asxsAutoDailyResetControl{
		SchemaVersion: 1,
		SiteID:        asxsAutoDailyResetControlSiteID,
		Enabled:       *request.Enabled,
		UpdatedAt:     time.Now().Unix(),
	}
	if err := writeASXSAutoDailyResetControl(control); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "保存 ASXS 自动重置开关失败",
		})
		return
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf(
		"ASXS automatic subscription reset operator control changed: site_id=%s enabled=%t",
		control.SiteID,
		control.Enabled,
	))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "ASXS 自动重置开关已更新",
		"data": gin.H{
			"configured": true,
			"site_id":    control.SiteID,
			"enabled":    control.Enabled,
			"updated_at": control.UpdatedAt,
		},
	})
}
