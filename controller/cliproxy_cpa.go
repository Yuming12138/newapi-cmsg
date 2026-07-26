package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const (
	defaultCliproxyCPABaseURL           = "http://cliproxy-api:8317"
	defaultCliproxyCPAManagementKeyFile = "/run/secrets/cliproxy_cpa_management_key"
)

type cliproxyCPAResetCreditRequest struct {
	AuthIndex       string `json:"auth_index"`
	RedeemRequestID string `json:"redeem_request_id,omitempty"`
}

func ConsumeCliproxyCPAResetCredit(c *gin.Context) {
	proxyCliproxyCPAManagementAuthAction(c, "/v0/management/consume-codex-reset-credit", "reset credit consumed")
}

func ResetCliproxyCPAQuotaState(c *gin.Context) {
	proxyCliproxyCPAManagementAuthAction(c, "/v0/management/reset-quota", "local quota state cleared")
}

func GetCliproxyCPADispatchAudits(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("invalid channel id: %w", err))
		return
	}

	ch, err := model.GetChannelById(channelID, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if ch == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel not found"})
		return
	}
	if !channelHasCliproxyCPAGuard(ch) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel is not managed by CLIProxy quota guard"})
		return
	}

	managementKey, err := cliproxyCPAManagementKey()
	if err != nil {
		common.SysError("cliproxy cpa dispatch audits unavailable: " + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "CPA management key is not configured"})
		return
	}

	endpoint, err := cliproxyCPAManagementURL("/v0/management/dispatch-audits")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	values := endpoint.Query()
	if limit := strings.TrimSpace(c.Query("limit")); limit != "" {
		values.Set("limit", limit)
	}
	if requestID := strings.TrimSpace(c.Query("request_id")); requestID != "" {
		values.Set("request_id", requestID)
	}
	endpoint.RawQuery = values.Encode()

	httpReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, endpoint.String(), nil)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+managementKey)
	httpReq.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		common.SysError("cliproxy cpa dispatch audits request failed: " + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "CPA dispatch audit request failed"})
		return
	}
	defer resp.Body.Close()

	var upstream map[string]any
	if err := common.DecodeJson(resp.Body, &upstream); err != nil {
		common.SysError("cliproxy cpa dispatch audits response decode failed: " + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "CPA dispatch audit response invalid"})
		return
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(fmt.Sprint(upstream["error"]))
		if message == "" {
			message = fmt.Sprintf("CPA dispatch audit request failed with status %d", resp.StatusCode)
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": message, "upstream_status": resp.StatusCode})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":         true,
		"upstream_status": resp.StatusCode,
		"data":            upstream,
	})
}

func proxyCliproxyCPAManagementAuthAction(c *gin.Context, managementPath string, successMessage string) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("invalid channel id: %w", err))
		return
	}

	var req cliproxyCPAResetCreditRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, fmt.Errorf("invalid request body: %w", err))
		return
	}
	authIndex := strings.TrimSpace(req.AuthIndex)
	if authIndex == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "auth_index is required"})
		return
	}

	ch, err := model.GetChannelById(channelID, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if ch == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel not found"})
		return
	}
	if !channelHasCliproxyCPAGuard(ch) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel is not managed by CLIProxy quota guard"})
		return
	}

	managementKey, err := cliproxyCPAManagementKey()
	if err != nil {
		common.SysError("cliproxy cpa reset credit unavailable: " + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "CPA management key is not configured"})
		return
	}

	payloadData := gin.H{"auth_index": authIndex}
	if redeemRequestID := strings.TrimSpace(req.RedeemRequestID); redeemRequestID != "" {
		payloadData["redeem_request_id"] = redeemRequestID
	}
	payload, err := common.Marshal(payloadData)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	endpoint, err := cliproxyCPAManagementURL(managementPath)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	httpReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+managementKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		common.SysError("cliproxy cpa reset credit request failed: " + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "CPA reset request failed"})
		return
	}
	defer resp.Body.Close()

	var upstream map[string]any
	if err := common.DecodeJson(resp.Body, &upstream); err != nil {
		common.SysError("cliproxy cpa reset credit response decode failed: " + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "CPA reset response invalid"})
		return
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(fmt.Sprint(upstream["error"]))
		if message == "" {
			message = fmt.Sprintf("CPA reset failed with status %d", resp.StatusCode)
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": message, "upstream_status": resp.StatusCode})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":         true,
		"message":         successMessage,
		"upstream_status": resp.StatusCode,
		"data":            upstream,
	})
}

func cliproxyCPAManagementURL(managementPath string) (*url.URL, error) {
	baseURL := strings.TrimSpace(os.Getenv("CLIPROXY_CPA_BASE_URL"))
	if baseURL == "" {
		baseURL = defaultCliproxyCPABaseURL
	}
	endpoint, err := url.JoinPath(strings.TrimRight(baseURL, "/"), managementPath)
	if err != nil {
		return nil, err
	}
	return url.Parse(endpoint)
}

func channelHasCliproxyCPAGuard(ch *model.Channel) bool {
	if ch == nil {
		return false
	}
	otherInfo := ch.GetOtherInfo()
	if _, ok := otherInfo["cliproxy_cpa_quota_guard"].(map[string]interface{}); ok {
		return true
	}
	return false
}

func cliproxyCPAManagementKey() (string, error) {
	if value := strings.TrimSpace(os.Getenv("CLIPROXY_CPA_MANAGEMENT_KEY")); value != "" {
		return value, nil
	}
	path := strings.TrimSpace(os.Getenv("CLIPROXY_CPA_MANAGEMENT_KEY_FILE"))
	if path == "" {
		path = defaultCliproxyCPAManagementKeyFile
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("empty management key file")
	}
	return value, nil
}
