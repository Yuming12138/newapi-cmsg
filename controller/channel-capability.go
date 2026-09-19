package controller

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const candyCapabilityPrompt = "Solve this problem carefully without external tools. A black bag contains candies with three flavors and two shapes. The counts are: apple round 7, apple star 7, peach round 9, peach star 6, watermelon round 8, watermelon star 4. What is the minimum number of candies to draw to guarantee having apple and peach candies of different shapes? End with exactly FINAL_ANSWER: <number> on its own line."

var capabilityModelPreference = []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5"}

var capabilityReasoningEfforts = map[string]struct{}{
	"none": {}, "minimal": {}, "low": {}, "medium": {},
	"high": {}, "xhigh": {}, "max": {}, "ultra": {},
}

func normalizeCapabilityReasoningEffort(raw string) (string, bool) {
	effort := strings.ToLower(strings.TrimSpace(raw))
	if effort == "" || effort == "auto" {
		return "", true
	}
	_, ok := capabilityReasoningEfforts[effort]
	return effort, ok
}

func selectCapabilityModel(channel *model.Channel, requested string) string {
	if strings.TrimSpace(requested) != "" {
		return strings.TrimSpace(requested)
	}
	models := channel.GetModels()
	available := make(map[string]bool, len(models))
	for _, item := range models {
		available[strings.TrimSpace(item)] = true
	}
	for _, preferred := range capabilityModelPreference {
		if available[preferred] {
			return preferred
		}
	}
	if len(models) > 0 {
		return strings.TrimSpace(models[0])
	}
	return "gpt-5.6-sol"
}

func capabilityAnswerMatches(text string) bool {

	return strings.HasSuffix(strings.TrimSpace(text), "FINAL_ANSWER: 21")
}

func capabilityAnswerPreview(text string) string {

	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > 4000 {
		return string(runes[:4000]) + "…"
	}
	return text
}

func capabilityFailureReason(ok bool, text string) string {
	if ok {
		return ""
	}
	if strings.TrimSpace(text) == "" {
		return "empty_response"
	}
	if !strings.Contains(text, "FINAL_ANSWER:") {
		return "missing_final_answer_marker"
	}
	return "answer_mismatch"
}

func TestChannelCapability(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || (id != 1 && id != 12 && id != 27) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "capability probe only supports channels 1, 12, and 27"})
		return
	}
	ch, err := model.CacheGetChannel(id)
	if err != nil {
		ch, err = model.GetChannelById(id, true)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	name := selectCapabilityModel(ch, c.Query("model"))
	effort, validEffort := normalizeCapabilityReasoningEffort(c.Query("effort"))
	if !validEffort {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "unsupported reasoning effort"})
		return
	}
	uid, err := resolveChannelTestUserID(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	started := time.Now()
	result := testChannelWithPrompt(ch, uid, name, "", false, candyCapabilityPrompt, effort)
	text := gjson.GetBytes(result.responseBody, "choices.0.message.content").String()
	if text == "" {
		text = gjson.GetBytes(result.responseBody, "output_text").String()
	}
	ok := capabilityAnswerMatches(text)
	status := "failed"
	if ok {
		status = "passed"
	}
	if result.localErr != nil {
		common.SysLog(fmt.Sprintf("capability probe channel=%d model=%s provider=%s status=unhealthy latency_ms=%d reason=%s", id, name, constant.GetChannelTypeName(ch.Type), time.Since(started).Milliseconds(), result.localErr.Error()))
		c.JSON(http.StatusOK, gin.H{"success": false, "channel_id": id, "requested_model": name, "reasoning_effort": effort, "quality_pass": false, "status": "unhealthy", "failure_reason": "upstream_error", "reason": result.localErr.Error(), "latency_ms": time.Since(started).Milliseconds()})
		return
	}
	common.SysLog(fmt.Sprintf("capability probe channel=%d requested_model=%s observed_model=%s provider=%s status=%s quality_pass=%t latency_ms=%d", id, name, result.upstreamModel, constant.GetChannelTypeName(ch.Type), status, ok, time.Since(started).Milliseconds()))
	c.JSON(http.StatusOK, gin.H{"success": true, "channel_id": id, "requested_model": name, "reasoning_effort": effort, "observed_model": result.upstreamModel, "provider": constant.GetChannelTypeName(ch.Type), "channel_name": ch.Name, "quality_pass": ok, "answer_match": ok, "fallback_used": false, "status": status, "failure_reason": capabilityFailureReason(ok, text), "answer_preview": capabilityAnswerPreview(text), "latency_ms": time.Since(started).Milliseconds()})
}
