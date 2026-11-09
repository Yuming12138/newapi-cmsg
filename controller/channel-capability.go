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

const pelicanCapabilityPrompt = `Create a polished, richly detailed self-contained SVG illustration of a pelican riding a bicycle along a coastal road. Use layered gradients, soft shadows, highlights, detailed feathers, a long articulated beak, wings, and legs that connect naturally to the pedals. Draw a convincing bicycle with two large spoked wheels, a frame, chain, crank and handlebars. Add a coastal background with sky, clouds, sea, waves, road and foreground details. Use smooth looping SVG/CSS animations for wheel rotation, crank and pedal motion, leg pedaling, body bobbing and background motion. Use a wide 16:9 viewBox, a balanced composition and a polished color palette. Return only the complete <svg>...</svg> document with no Markdown fence and no explanation.`

const capabilityProbeMaxOutputTokens = uint(24000)

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

const maxCapabilitySVGRunes = 120000

// extractCapabilitySVG accepts both a bare SVG response and an SVG wrapped in
// prose/Markdown. Only the first complete SVG root is returned to the UI.
func extractCapabilitySVG(text string) string {
	lower := strings.ToLower(text)
	start := strings.Index(lower, "<svg")
	if start < 0 {
		return ""
	}
	endRelative := strings.Index(lower[start:], "</svg>")
	if endRelative < 0 {
		return ""
	}
	end := start + endRelative + len("</svg>")
	svg := strings.TrimSpace(text[start:end])
	if len([]rune(svg)) > maxCapabilitySVGRunes {
		return ""
	}
	return svg
}

func capabilityAnswerMatches(text string) bool {
	return extractCapabilitySVG(text) != ""
}

type capabilitySVGMetrics struct {
	ViewBox    string `json:"view_box"`
	Paths      int    `json:"paths"`
	Groups     int    `json:"groups"`
	Gradients  int    `json:"gradients"`
	Animations int    `json:"animations"`
	Bytes      int    `json:"bytes"`
}

func capabilitySVGAttribute(svg, name string) string {
	lower := strings.ToLower(svg)
	needle := strings.ToLower(name)
	index := strings.Index(lower, needle)
	if index < 0 {
		return ""
	}
	rest := svg[index+len(needle):]
	equals := strings.Index(rest, "=")
	if equals < 0 {
		return ""
	}
	rest = strings.TrimSpace(rest[equals+1:])
	if rest == "" {
		return ""
	}
	quote := rest[0]
	if quote != '\'' && quote != '"' {
		return ""
	}
	end := strings.IndexByte(rest[1:], quote)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[1 : end+1])
}

func countCapabilitySVGToken(svg, token string) int {
	return strings.Count(strings.ToLower(svg), strings.ToLower(token))
}

func inspectCapabilitySVG(svg string) capabilitySVGMetrics {
	lower := strings.ToLower(svg)
	animations := countCapabilitySVGToken(lower, "<animate") + countCapabilitySVGToken(lower, "@keyframes")
	if strings.Contains(lower, "animation:") && animations == 0 {
		animations = 1
	}
	return capabilitySVGMetrics{
		ViewBox:    capabilitySVGAttribute(svg, "viewBox"),
		Paths:      countCapabilitySVGToken(lower, "<path"),
		Groups:     countCapabilitySVGToken(lower, "<g"),
		Gradients:  countCapabilitySVGToken(lower, "<lineargradient") + countCapabilitySVGToken(lower, "<radialgradient"),
		Animations: animations,
		Bytes:      len(svg),
	}
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
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "<svg") {
		return "missing_svg"
	}
	if !strings.Contains(lower, "</svg>") {
		return "incomplete_svg"
	}
	return "invalid_svg"
}

func capabilityResponseText(responseBody []byte) string {
	for _, path := range []string{"choices.0.message.content", "output_text"} {
		value := gjson.GetBytes(responseBody, path)
		if value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
			return value.String()
		}
		if value.IsArray() {
			var parts []string
			for _, item := range value.Array() {
				if item.Type == gjson.String {
					parts = append(parts, item.String())
					continue
				}
				if text := item.Get("text").String(); text != "" {
					parts = append(parts, text)
				}
			}
			if text := strings.Join(parts, ""); strings.TrimSpace(text) != "" {
				return text
			}
		}
	}

	// Native Responses JSON normally stores text under output[].content[].text.
	var parts []string
	for _, output := range gjson.GetBytes(responseBody, "output").Array() {
		for _, content := range output.Get("content").Array() {
			if text := content.Get("text").String(); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "")
}

func parseCapabilityTestChannelID(raw string) (int, bool) {
	id, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func TestChannelCapability(c *gin.Context) {
	id, validID := parseCapabilityTestChannelID(c.Param("id"))
	if !validID {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid channel id"})
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
	endpointType := capabilityTestEndpoint(ch, name)
	started := time.Now()
	result := testChannelWithPrompt(ch, uid, name, endpointType, false, pelicanCapabilityPrompt, effort)
	text := capabilityResponseText(result.responseBody)
	svg := extractCapabilitySVG(text)
	ok := capabilityAnswerMatches(text)
	metrics := inspectCapabilitySVG(svg)
	status := "failed"
	if ok {
		status = "passed"
	}
	if result.localErr != nil {
		common.SysLog(fmt.Sprintf("capability probe channel=%d model=%s provider=%s status=unhealthy latency_ms=%d reason=%s", id, name, constant.GetChannelTypeName(ch.Type), time.Since(started).Milliseconds(), result.localErr.Error()))
		c.JSON(http.StatusOK, gin.H{"success": false, "channel_id": id, "requested_model": name, "reasoning_effort": effort, "quality_pass": false, "status": "unhealthy", "failure_reason": "upstream_error", "reason": result.localErr.Error(), "latency_ms": time.Since(started).Milliseconds()})
		return
	}
	common.SysLog(fmt.Sprintf("capability probe channel=%d requested_model=%s observed_model=%s provider=%s status=%s svg_valid=%t latency_ms=%d", id, name, result.upstreamModel, constant.GetChannelTypeName(ch.Type), status, ok, time.Since(started).Milliseconds()))
	c.JSON(http.StatusOK, gin.H{
		"success":          true,
		"channel_id":       id,
		"requested_model":  name,
		"reasoning_effort": effort,
		"observed_model":   result.upstreamModel,
		"provider":         constant.GetChannelTypeName(ch.Type),
		"channel_name":     ch.Name,
		"quality_pass":     ok,
		"structure_pass":   ok,
		"answer_match":     ok,
		"evaluation_scope": "single_turn_no_tools",
		"fallback_used":    false,
		"status":           status,
		"failure_reason":   capabilityFailureReason(ok, text),
		"answer_preview":   capabilityAnswerPreview(text),
		"svg_detected":     svg != "",
		"svg_preview":      svg,
		"svg_metrics":      metrics,
		"latency_ms":       time.Since(started).Milliseconds(),
	})
}

// capabilityTestEndpoint picks the protocol route a channel actually serves for
// the selected model. Advanced Custom channels commonly expose only a
// Responses-to-Chat route, so probing them through Chat Completions would test
// a different path (and hit its short token cap) instead of the production path.
func capabilityTestEndpoint(channel *model.Channel, modelName string) string {
	if channel == nil || channel.Type != constant.ChannelTypeAdvancedCustom {
		return ""
	}
	settings := channel.GetOtherSettings()
	if settings.AdvancedCustom != nil && settings.AdvancedCustom.SupportsPath("/v1/responses") {
		return string(constant.EndpointTypeOpenAIResponse)
	}
	return ""
}
