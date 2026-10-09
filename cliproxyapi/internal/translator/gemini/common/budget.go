package common

import (
	"encoding/json"
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	// AntigravityHardTokenLimit is the strict upper bound on input tokens enforced by Google CloudCode Antigravity API.
	AntigravityHardTokenLimit = 1048576

	// AntigravityDefaultMaxTokens is the safe target ceiling for total request tokens.
	// 850,000 tokens leaves ~200,000 tokens (~800KB) of safety margin for tokenizer differences.
	AntigravityDefaultMaxTokens = 850000

	// AntigravityFastPathByteThreshold: requests smaller than 1,000,000 bytes (1MB) cannot exceed 850,000 tokens.
	AntigravityFastPathByteThreshold = 1000000

	// MaxHistoricalToolOutputLen is the maximum character length for historical tool responses before truncation.
	MaxHistoricalToolOutputLen  = 2000
	HistoricalToolOutputHeadLen = 1000
	HistoricalToolOutputTailLen = 1000

	TruncatedToolOutputNotice = "\n[... tool output truncated to stay within context budget ...]\n"
	TruncatedHistoryNotice    = "[Earlier conversation history was pruned to stay within context limits.]\n\n"
)

// EstimateTextTokens returns a conservative estimate of the token count for a text string.
// For ASCII characters: roughly 3.0 characters per token.
// For non-ASCII (e.g. CJK): roughly 1.5 tokens per rune.
func EstimateTextTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	var asciiCount int
	var nonAsciiCount int
	for _, r := range text {
		if r < 128 {
			asciiCount++
		} else {
			nonAsciiCount++
		}
	}
	tokens := (asciiCount+2)/3 + int(float64(nonAsciiCount)*1.5)
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

// EstimateGeminiPartTokens calculates estimated tokens for a single Gemini part.
func EstimateGeminiPartTokens(part gjson.Result) int {
	if !part.Exists() {
		return 0
	}
	if text := part.Get("text"); text.Exists() {
		return EstimateTextTokens(text.String())
	}
	if fc := part.Get("functionCall"); fc.Exists() {
		return EstimateTextTokens(fc.Raw) + 10
	}
	if fr := part.Get("functionResponse"); fr.Exists() {
		res := fr.Get("response.result")
		if !res.Exists() {
			res = fr.Get("response")
		}
		if res.Exists() {
			if res.Type == gjson.String {
				return EstimateTextTokens(res.String()) + 10
			}
			return EstimateTextTokens(res.Raw) + 10
		}
		return EstimateTextTokens(fr.Raw) + 10
	}
	if fr := part.Get("function_response"); fr.Exists() {
		res := fr.Get("response.result")
		if !res.Exists() {
			res = fr.Get("response")
		}
		if res.Exists() {
			if res.Type == gjson.String {
				return EstimateTextTokens(res.String()) + 10
			}
			return EstimateTextTokens(res.Raw) + 10
		}
		return EstimateTextTokens(fr.Raw) + 10
	}
	if part.Get("inline_data").Exists() {
		return 258 // Standard Gemini image token cost
	}
	return EstimateTextTokens(part.Raw)
}

// EstimateGeminiContentTokens estimates tokens for a single message turn.
func EstimateGeminiContentTokens(content gjson.Result) int {
	if !content.Exists() {
		return 0
	}
	tokens := 4 // Overhead for message boundary and role
	parts := content.Get("parts")
	if parts.IsArray() {
		parts.ForEach(func(_, p gjson.Result) bool {
			tokens += EstimateGeminiPartTokens(p)
			return true
		})
	}
	return tokens
}

// EstimateGeminiPayloadTokens estimates total tokens in system instruction, tools, and contents.
func EstimateGeminiPayloadTokens(rawJSON []byte, contentsPath string) int {
	tokens := 0

	// 1. System instructions
	sysPath := "systemInstruction"
	if strings.HasPrefix(contentsPath, "request.") {
		sysPath = "request.systemInstruction"
	}
	if sys := gjson.GetBytes(rawJSON, sysPath); sys.Exists() {
		if parts := sys.Get("parts"); parts.IsArray() {
			parts.ForEach(func(_, p gjson.Result) bool {
				tokens += EstimateGeminiPartTokens(p)
				return true
			})
		}
	}

	// 2. Tools
	toolsPath := "tools"
	if strings.HasPrefix(contentsPath, "request.") {
		toolsPath = "request.tools"
	}
	if tools := gjson.GetBytes(rawJSON, toolsPath); tools.Exists() {
		tokens += EstimateTextTokens(tools.Raw)
	}

	// 3. Contents
	if contents := gjson.GetBytes(rawJSON, contentsPath); contents.Exists() && contents.IsArray() {
		contents.ForEach(func(_, c gjson.Result) bool {
			tokens += EstimateGeminiContentTokens(c)
			return true
		})
	}

	return tokens
}

// ResolveContentsPath identifies whether the contents array is at root or inside "request.".
func ResolveContentsPath(rawJSON []byte, preferredPath string) string {
	if preferredPath != "" && gjson.GetBytes(rawJSON, preferredPath).Exists() {
		return preferredPath
	}
	if gjson.GetBytes(rawJSON, "request.contents").Exists() {
		return "request.contents"
	}
	if gjson.GetBytes(rawJSON, "contents").Exists() {
		return "contents"
	}
	return preferredPath
}

func hasToolResponse(content gjson.Result) bool {
	parts := content.Get("parts")
	if !parts.IsArray() {
		return false
	}
	has := false
	parts.ForEach(func(_, p gjson.Result) bool {
		if p.Get("functionResponse").Exists() || p.Get("function_response").Exists() {
			has = true
			return false
		}
		return true
	})
	return has
}

func hasToolCall(content gjson.Result) bool {
	parts := content.Get("parts")
	if !parts.IsArray() {
		return false
	}
	has := false
	parts.ForEach(func(_, p gjson.Result) bool {
		if p.Get("functionCall").Exists() || p.Get("function_call").Exists() {
			has = true
			return false
		}
		return true
	})
	return has
}

// CompactHistoricalToolOutputs truncates oversized tool outputs in historical turns
// (excluding the last turn, which is the immediate turn the model should respond to).
func CompactHistoricalToolOutputs(rawJSON []byte, contentsPath string) ([]byte, bool) {
	contents := gjson.GetBytes(rawJSON, contentsPath)
	if !contents.Exists() || !contents.IsArray() {
		return rawJSON, false
	}
	arr := contents.Array()
	if len(arr) <= 1 {
		return rawJSON, false
	}

	modified := false
	// Process turns 0 through len(arr)-2
	for turnIdx := 0; turnIdx < len(arr)-1; turnIdx++ {
		turn := arr[turnIdx]
		parts := turn.Get("parts")
		if !parts.Exists() || !parts.IsArray() {
			continue
		}
		partsArr := parts.Array()
		for partIdx := 0; partIdx < len(partsArr); partIdx++ {
			part := partsArr[partIdx]
			fr := part.Get("functionResponse")
			frKey := "functionResponse"
			if !fr.Exists() {
				fr = part.Get("function_response")
				frKey = "function_response"
			}
			if !fr.Exists() {
				continue
			}

			res := fr.Get("response.result")
			resPath := fmt.Sprintf("%s.%d.parts.%d.%s.response.result", contentsPath, turnIdx, partIdx, frKey)
			if !res.Exists() {
				res = fr.Get("response")
				resPath = fmt.Sprintf("%s.%d.parts.%d.%s.response", contentsPath, turnIdx, partIdx, frKey)
			}
			if !res.Exists() {
				continue
			}

			var origStr string
			if res.Type == gjson.String {
				origStr = res.String()
			} else {
				origStr = res.Raw
			}

			if len(origStr) > MaxHistoricalToolOutputLen {
				head := origStr[:HistoricalToolOutputHeadLen]
				tail := origStr[len(origStr)-HistoricalToolOutputTailLen:]
				truncated := head + TruncatedToolOutputNotice + tail

				var errSet error
				rawJSON, errSet = sjson.SetBytes(rawJSON, resPath, truncated)
				if errSet == nil {
					modified = true
				}
			}
		}
	}
	return rawJSON, modified
}

// PruneOldestTurns removes the oldest turns from contentsPath until estimated tokens <= maxTokens.
// It maintains conversational integrity:
// 1. The first retained turn has role "user".
// 2. Tool calls and tool responses are preserved or removed in matched pairs.
func PruneOldestTurns(rawJSON []byte, contentsPath string, maxTokens int) []byte {
	contents := gjson.GetBytes(rawJSON, contentsPath)
	if !contents.Exists() || !contents.IsArray() {
		return rawJSON
	}
	arr := contents.Array()
	if len(arr) <= 2 {
		return truncateLeadingTurnIfOversized(rawJSON, contentsPath, maxTokens)
	}

	// Strategy 1: Find user message cut-points.
	// A cut-point i means arr[i] is a genuine user message (role="user" and no tool response).
	cutPoints := make([]int, 0)
	for i := 1; i < len(arr); i++ {
		turn := arr[i]
		if turn.Get("role").String() == "user" && !hasToolResponse(turn) {
			cutPoints = append(cutPoints, i)
		}
	}

	for _, cutIdx := range cutPoints {
		remaining := arr[cutIdx:]
		if len(remaining) == 0 {
			break
		}

		remainingRaw := make([]any, 0, len(remaining))
		for idx, item := range remaining {
			var itemObj any
			if err := json.Unmarshal([]byte(item.Raw), &itemObj); err != nil {
				continue
			}
			if idx == 0 {
				if itemMap, ok := itemObj.(map[string]any); ok {
					if parts, ok := itemMap["parts"].([]any); ok && len(parts) > 0 {
						if firstPart, ok := parts[0].(map[string]any); ok {
							if text, ok := firstPart["text"].(string); ok {
								if !strings.HasPrefix(text, "[Earlier conversation history") {
									firstPart["text"] = TruncatedHistoryNotice + text
								}
							}
						}
					}
				}
			}
			remainingRaw = append(remainingRaw, itemObj)
		}

		candidateJSON, err := sjson.SetBytes(rawJSON, contentsPath, remainingRaw)
		if err != nil {
			continue
		}

		tokens := EstimateGeminiPayloadTokens(candidateJSON, contentsPath)
		rawJSON = candidateJSON
		if tokens <= maxTokens {
			return rawJSON
		}
	}

	// Strategy 2: If there are no earlier user-message cut points (e.g. 1 user goal + many tool turns),
	// prune older tool-call (model) and tool-response (user/function) pairs:
	// arr[0] is user goal.
	// arr[1] is model tool call.
	// arr[2] is user tool response.
	arr = gjson.GetBytes(rawJSON, contentsPath).Array()
	for len(arr) > 4 {
		tokens := EstimateGeminiPayloadTokens(rawJSON, contentsPath)
		if tokens <= maxTokens {
			break
		}

		if hasToolCall(arr[1]) && hasToolResponse(arr[2]) {
			newArr := make([]any, 0, len(arr)-2)
			var firstObj any
			_ = json.Unmarshal([]byte(arr[0].Raw), &firstObj)
			newArr = append(newArr, firstObj)
			for _, item := range arr[3:] {
				var obj any
				_ = json.Unmarshal([]byte(item.Raw), &obj)
				newArr = append(newArr, obj)
			}
			candidateJSON, err := sjson.SetBytes(rawJSON, contentsPath, newArr)
			if err == nil {
				rawJSON = candidateJSON
				arr = gjson.GetBytes(rawJSON, contentsPath).Array()
				continue
			}
		}
		break
	}

	return truncateLeadingTurnIfOversized(rawJSON, contentsPath, maxTokens)
}

func truncateLeadingTurnIfOversized(rawJSON []byte, contentsPath string, maxTokens int) []byte {
	tokens := EstimateGeminiPayloadTokens(rawJSON, contentsPath)
	if tokens <= maxTokens {
		return rawJSON
	}

	contents := gjson.GetBytes(rawJSON, contentsPath)
	if !contents.Exists() || !contents.IsArray() {
		return rawJSON
	}
	arr := contents.Array()
	if len(arr) == 0 {
		return rawJSON
	}

	// Truncate text in the first user turn if it's massively oversized
	maxChars := (maxTokens - 50000) * 3
	if maxChars < 10000 {
		maxChars = 10000
	}

	firstTurn := arr[0]
	parts := firstTurn.Get("parts")
	if parts.IsArray() && len(parts.Array()) > 0 {
		firstPart := parts.Array()[0]
		if text := firstPart.Get("text"); text.Exists() {
			s := text.String()
			if len(s) > maxChars {
				truncated := s[:maxChars] + "\n\n[Message truncated: content exceeded context budget]"
				path := fmt.Sprintf("%s.0.parts.0.text", contentsPath)
				rawJSON, _ = sjson.SetBytes(rawJSON, path, truncated)
			}
		}
	}

	return rawJSON
}

// EnsureAntigravityBudget checks the estimated token count of a Gemini request.
// If it exceeds AntigravityDefaultMaxTokens (850,000), it applies:
// 1. Tool output compaction (truncating oversized historical tool outputs).
// 2. Conversational turn pruning (dropping oldest turns while preserving structural validity).
func EnsureAntigravityBudget(rawJSON []byte, contentsPath string, modelName string) []byte {
	if len(rawJSON) < AntigravityFastPathByteThreshold {
		return rawJSON
	}

	contentsPath = ResolveContentsPath(rawJSON, contentsPath)
	contents := gjson.GetBytes(rawJSON, contentsPath)
	if !contents.Exists() || !contents.IsArray() || len(contents.Array()) == 0 {
		return rawJSON
	}

	currentTokens := EstimateGeminiPayloadTokens(rawJSON, contentsPath)
	if currentTokens <= AntigravityDefaultMaxTokens {
		return rawJSON
	}

	log.Infof("antigravity budget: request exceeds safe token limit (estimated %d tokens > %d), compacting context for model %s",
		currentTokens, AntigravityDefaultMaxTokens, modelName)

	rawJSON, modified := CompactHistoricalToolOutputs(rawJSON, contentsPath)
	if modified {
		currentTokens = EstimateGeminiPayloadTokens(rawJSON, contentsPath)
		if currentTokens <= AntigravityDefaultMaxTokens {
			log.Infof("antigravity budget: tool output compaction brought request down to %d tokens", currentTokens)
			return rawJSON
		}
	}

	rawJSON = PruneOldestTurns(rawJSON, contentsPath, AntigravityDefaultMaxTokens)
	currentTokens = EstimateGeminiPayloadTokens(rawJSON, contentsPath)
	log.Infof("antigravity budget: turn pruning finished, final estimated tokens: %d", currentTokens)

	return rawJSON
}
