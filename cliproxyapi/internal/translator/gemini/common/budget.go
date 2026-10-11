package common

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	AntigravityHardTokenLimit = 1048576
	// A heuristic ceiling with roughly 300K tokens reserved for tokenizer
	// differences. The executor must also check the final outgoing payload.
	AntigravityDefaultMaxTokens = 750000
	// Byte length is not a token count. Only small requests skip estimation.
	AntigravityFastPathByteThreshold = 256000

	MaxHistoricalToolOutputLen  = 2000
	HistoricalToolOutputHeadLen = 1000
	HistoricalToolOutputTailLen = 1000
	// Used only when pruning/compacting history cannot meet the budget.
	MaxActiveToolOutputLen = 16000

	TruncatedToolOutputNotice = "\n[... tool output truncated to stay within context budget ...]\n"
	TruncatedHistoryNotice    = "[Earlier conversation history was pruned to stay within context limits.]\n\n"
	TruncatedMessageNotice    = "\n[... message truncated to stay within context budget ...]\n"
)

// EstimateTextTokens estimates two ASCII bytes per token and 1.5 tokens per
// non-ASCII rune. Code and patches must not use the old prose-oriented 3:1
// ratio. This is still a heuristic, not Google's tokenizer.
func EstimateTextTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	ascii := 0
	for i := 0; i < len(text); i++ {
		if text[i] < utf8.RuneSelf {
			ascii++
		}
	}
	if ascii == len(text) {
		return (ascii + 1) / 2
	}
	nonASCII := 0
	for _, r := range text {
		if r >= utf8.RuneSelf {
			nonASCII++
		}
	}
	return (ascii+1)/2 + (nonASCII*3+1)/2
}

// EstimateGeminiPartTokens includes function metadata and the whole response,
// not only response.result (other response fields can also be large).
func EstimateGeminiPartTokens(part gjson.Result) int {
	if !part.Exists() {
		return 0
	}
	if text := part.Get("text"); text.Exists() {
		return EstimateTextTokens(text.String())
	}
	for _, key := range []string{"functionCall", "function_call", "functionResponse", "function_response"} {
		if value := part.Get(key); value.Exists() {
			return EstimateTextTokens(value.Raw) + 10
		}
	}
	if part.Get("inline_data").Exists() || part.Get("inlineData").Exists() {
		return 258 // Multimodal costs remain estimates; upstream rejection is handled separately.
	}
	return EstimateTextTokens(part.Raw)
}

func EstimateGeminiContentTokens(content gjson.Result) int {
	if !content.Exists() {
		return 0
	}
	tokens := 4
	for _, part := range content.Get("parts").Array() {
		tokens += EstimateGeminiPartTokens(part)
	}
	return tokens
}

// EstimateGeminiPayloadTokens accounts for fixed instructions/schemas as well
// as conversation history. Fixed inputs are never silently truncated.
func EstimateGeminiPayloadTokens(rawJSON []byte, contentsPath string) int {
	prefix := ""
	if strings.HasPrefix(contentsPath, "request.") {
		prefix = "request."
	}
	tokens := 0
	for _, key := range []string{"systemInstruction", "system_instruction"} {
		for _, part := range gjson.GetBytes(rawJSON, prefix+key+".parts").Array() {
			tokens += EstimateGeminiPartTokens(part)
		}
	}
	for _, key := range []string{"tools", "generationConfig.responseJsonSchema", "generationConfig.responseSchema"} {
		if value := gjson.GetBytes(rawJSON, prefix+key); value.Exists() {
			tokens += EstimateTextTokens(value.Raw)
		}
	}
	for _, content := range gjson.GetBytes(rawJSON, contentsPath).Array() {
		tokens += EstimateGeminiContentTokens(content)
	}
	return tokens
}

func ResolveContentsPath(rawJSON []byte, preferredPath string) string {
	if preferredPath != "" && gjson.GetBytes(rawJSON, preferredPath).Exists() {
		return preferredPath
	}
	for _, path := range []string{"request.contents", "contents"} {
		if gjson.GetBytes(rawJSON, path).Exists() {
			return path
		}
	}
	return preferredPath
}

func hasToolResponse(content gjson.Result) bool {
	for _, part := range content.Get("parts").Array() {
		if part.Get("functionResponse").Exists() || part.Get("function_response").Exists() {
			return true
		}
	}
	return false
}

func hasToolCall(content gjson.Result) bool {
	for _, part := range content.Get("parts").Array() {
		if part.Get("functionCall").Exists() || part.Get("function_call").Exists() {
			return true
		}
	}
	return false
}

// CompactHistoricalToolOutputs intentionally leaves the last turn untouched.
// The budget pipeline has a separate last-resort active-output stage.
func CompactHistoricalToolOutputs(rawJSON []byte, contentsPath string) ([]byte, bool) {
	return compactToolOutputs(rawJSON, contentsPath, MaxHistoricalToolOutputLen, false)
}

// Rewrite each changed part/turn once and the request contents once. In
// particular, never call sjson on the whole request for each historical output.
func compactToolOutputs(rawJSON []byte, contentsPath string, maxBytes int, includeLast bool) ([]byte, bool) {
	contents := gjson.GetBytes(rawJSON, contentsPath)
	if !contents.IsArray() {
		return rawJSON, false
	}
	arr := contents.Array()
	end := len(arr)
	if !includeLast {
		end--
	}
	turns := make([]json.RawMessage, len(arr))
	modified := false
	for i, turn := range arr {
		turns[i] = json.RawMessage(turn.Raw)
		if i >= end {
			continue
		}
		partsArr := turn.Get("parts").Array()
		parts := make([]json.RawMessage, len(partsArr))
		changed := false
		for j, part := range partsArr {
			parts[j] = json.RawMessage(part.Raw)
			key := "functionResponse"
			response := part.Get(key + ".response")
			if !response.Exists() {
				key = "function_response"
				response = part.Get(key + ".response")
			}
			if !response.Exists() {
				continue
			}
			result := response.Get("result")
			path := key + ".response.result"
			// response.result is conventional, but some tools return large
			// sibling fields. Compact that object too instead of rejecting an
			// otherwise shrinkable tool output at the final executor guard.
			wholeResponse := !result.Exists() || len(response.Raw)-len(result.Raw) > maxBytes
			if wholeResponse {
				result = response
				path = key + ".response"
			}
			text := result.Raw
			if result.Type == gjson.String {
				text = result.String()
			}
			if len(text) <= maxBytes {
				continue
			}
			truncated := truncateBudgetText(text, maxBytes, TruncatedToolOutputNotice)
			if truncated == text {
				continue
			}
			var replacement any = truncated
			if wholeResponse {
				// Gemini requires response to remain an object.
				replacement = map[string]string{"result": truncated}
			}
			updated, err := sjson.SetBytes(parts[j], path, replacement)
			if err == nil {
				parts[j] = updated
				changed = true
			}
		}
		if changed {
			partsJSON, err := json.Marshal(parts)
			if err != nil {
				return rawJSON, false
			}
			updated, err := sjson.SetRawBytes(turns[i], "parts", partsJSON)
			if err != nil {
				return rawJSON, false
			}
			turns[i] = updated
			modified = true
		}
	}
	if !modified {
		return rawJSON, false
	}
	return replaceBudgetContents(rawJSON, contentsPath, turns), true
}

// Retain both ends without splitting UTF-8. The explicit notice is additional
// to maxBytes; recognizing already-truncated outputs makes the guard idempotent.
func truncateBudgetText(text string, maxBytes int, notice string) string {
	if len(text) <= maxBytes {
		return text
	}
	if len(text) <= maxBytes+len(notice) && strings.Contains(text, notice) {
		return text
	}
	headEnd := maxBytes / 2
	for headEnd > 0 && !utf8.RuneStart(text[headEnd]) {
		headEnd--
	}
	tailStart := len(text) - (maxBytes - maxBytes/2)
	for tailStart < len(text) && !utf8.RuneStart(text[tailStart]) {
		tailStart++
	}
	return text[:headEnd] + notice + text[tailStart:]
}

func replaceBudgetContents(rawJSON []byte, contentsPath string, turns []json.RawMessage) []byte {
	contentsJSON, err := json.Marshal(turns)
	if err != nil {
		return rawJSON
	}
	updated, err := sjson.SetRawBytes(rawJSON, contentsPath, contentsJSON)
	if err != nil {
		return rawJSON
	}
	return updated
}

// PruneOldestTurns removes conversational prefixes and matched old tool pairs,
// preserving the user goal and the active call/response pair. It estimates each
// turn once rather than reserializing/rescanning every candidate suffix.
func PruneOldestTurns(rawJSON []byte, contentsPath string, maxTokens int) []byte {
	pruned := pruneOldestTurns(rawJSON, contentsPath, maxTokens)
	if EstimateGeminiPayloadTokens(pruned, contentsPath) > maxTokens {
		pruned = compactOversizedMessageTexts(pruned, contentsPath, maxTokens)
	}
	return pruned
}

func pruneOldestTurns(rawJSON []byte, contentsPath string, maxTokens int) []byte {
	contents := gjson.GetBytes(rawJSON, contentsPath)
	if !contents.IsArray() {
		return rawJSON
	}
	arr := contents.Array()
	if len(arr) <= 2 {
		return rawJSON
	}
	turnTokens := make([]int, len(arr))
	contentTokens := 0
	for i, turn := range arr {
		turnTokens[i] = EstimateGeminiContentTokens(turn)
		contentTokens += turnTokens[i]
	}
	currentTokens := EstimateGeminiPayloadTokens(rawJSON, contentsPath)
	if currentTokens <= maxTokens {
		return rawJSON
	}
	fixedTokens := currentTokens - contentTokens
	remainingTokens := contentTokens
	cutIdx := 0
	for i := 1; i < len(arr); i++ {
		remainingTokens -= turnTokens[i-1]
		if arr[i].Get("role").String() != "user" || hasToolResponse(arr[i]) || hasToolCall(arr[i]) {
			continue
		}
		cutIdx = i
		if fixedTokens+remainingTokens+EstimateTextTokens(TruncatedHistoryNotice) <= maxTokens {
			break
		}
	}
	arr = arr[cutIdx:]
	turnTokens = turnTokens[cutIdx:]
	remainingTokens = 0
	for _, tokens := range turnTokens {
		remainingTokens += tokens
	}
	first := json.RawMessage(arr[0].Raw)
	if cutIdx > 0 {
		if text := arr[0].Get("parts.0.text"); text.Exists() && !strings.HasPrefix(text.String(), TruncatedHistoryNotice) {
			if updated, err := sjson.SetBytes(first, "parts.0.text", TruncatedHistoryNotice+text.String()); err == nil {
				first = updated
				remainingTokens += EstimateTextTokens(TruncatedHistoryNotice)
			}
		}
	}
	next := 1
	for fixedTokens+remainingTokens > maxTokens && len(arr)-next > 2 {
		if !matchingBudgetToolTurns(arr[next], arr[next+1]) {
			break
		}
		remainingTokens -= turnTokens[next] + turnTokens[next+1]
		next += 2
	}
	if cutIdx == 0 && next == 1 {
		return rawJSON
	}
	turns := make([]json.RawMessage, 0, len(arr)-next+1)
	turns = append(turns, first)
	for _, turn := range arr[next:] {
		turns = append(turns, json.RawMessage(turn.Raw))
	}
	return replaceBudgetContents(rawJSON, contentsPath, turns)
}

func matchingBudgetToolTurns(callTurn, responseTurn gjson.Result) bool {
	var calls, responses []gjson.Result
	for _, part := range callTurn.Get("parts").Array() {
		call := part.Get("functionCall")
		if !call.Exists() {
			call = part.Get("function_call")
		}
		if call.Exists() {
			calls = append(calls, call)
		}
	}
	for _, part := range responseTurn.Get("parts").Array() {
		response := part.Get("functionResponse")
		if !response.Exists() {
			response = part.Get("function_response")
		}
		if response.Exists() {
			responses = append(responses, response)
		}
	}
	if len(calls) == 0 || len(calls) != len(responses) {
		return false
	}
	for i, call := range calls {
		if call.Get("name").String() != responses[i].Get("name").String() || call.Get("id").String() != responses[i].Get("id").String() {
			return false
		}
	}
	return true
}

// Final fallback for oversized plain message parts, not just the first part
// of the first turn. Fixed instructions/schemas, call args and signed/thought
// parts are not rewritten. Those cases must be rejected by the executor if
// still over budget.
func compactOversizedMessageTexts(rawJSON []byte, contentsPath string, maxTokens int) []byte {
	arr := gjson.GetBytes(rawJSON, contentsPath).Array()
	totalTokens := EstimateGeminiPayloadTokens(rawJSON, contentsPath)
	textTokens := 0
	eligible := func(part gjson.Result) bool {
		text := part.Get("text")
		return text.Exists() && len(text.String()) > MaxActiveToolOutputLen && !part.Get("thought").Bool() &&
			!part.Get("thoughtSignature").Exists() && !part.Get("thought_signature").Exists()
	}
	for _, turn := range arr {
		for _, part := range turn.Get("parts").Array() {
			if eligible(part) {
				textTokens += EstimateTextTokens(part.Get("text").String())
			}
		}
	}
	available := maxTokens - (totalTokens - textTokens)
	if available <= 0 || textTokens == 0 {
		return rawJSON
	}
	turns := make([]json.RawMessage, len(arr))
	for i, turn := range arr {
		turns[i] = json.RawMessage(turn.Raw)
		partsArr := turn.Get("parts").Array()
		parts := make([]json.RawMessage, len(partsArr))
		changed := false
		for j, part := range partsArr {
			parts[j] = json.RawMessage(part.Raw)
			if !eligible(part) {
				continue
			}
			text := part.Get("text").String()
			share := int(int64(available) * int64(EstimateTextTokens(text)) / int64(textTokens))
			// One UTF-8 byte per token is a deliberately conservative
			// allowance for last-resort truncation, notice included.
			maxBytes := share - len(TruncatedMessageNotice)
			if maxBytes < 0 {
				maxBytes = 0
			}
			updated, err := sjson.SetBytes(parts[j], "text", truncateBudgetText(text, maxBytes, TruncatedMessageNotice))
			if err == nil {
				parts[j] = updated
				changed = true
			}
		}
		if changed {
			partsJSON, err := json.Marshal(parts)
			if err == nil {
				if updated, err := sjson.SetRawBytes(turns[i], "parts", partsJSON); err == nil {
					turns[i] = updated
				}
			}
		}
	}
	return replaceBudgetContents(rawJSON, contentsPath, turns)
}

// EnsureAntigravityBudget compacts history first, then active outputs/oversized
// plain text only if needed. It cannot safely shrink arbitrary schemas or call
// arguments; the executor checks the result and rejects an unshrinkable request.
func EnsureAntigravityBudget(rawJSON []byte, contentsPath string, modelName string) []byte {
	return EnsureAntigravityBudgetWithLimit(rawJSON, contentsPath, modelName, AntigravityDefaultMaxTokens)
}

// EnsureAntigravityBudgetWithLimit supports a smaller budget for one recovery
// attempt after the upstream tokenizer rejects the initial request.
func EnsureAntigravityBudgetWithLimit(rawJSON []byte, contentsPath string, modelName string, maxTokens int) []byte {
	if maxTokens <= 0 {
		return rawJSON
	}
	if len(rawJSON) < AntigravityFastPathByteThreshold && len(rawJSON) < maxTokens/2 {
		return rawJSON
	}
	contentsPath = ResolveContentsPath(rawJSON, contentsPath)
	contents := gjson.GetBytes(rawJSON, contentsPath)
	if !contents.IsArray() || len(contents.Array()) == 0 {
		return rawJSON
	}
	currentTokens := EstimateGeminiPayloadTokens(rawJSON, contentsPath)
	if currentTokens <= maxTokens {
		return rawJSON
	}
	log.WithFields(log.Fields{"model": modelName, "estimated_tokens": currentTokens, "budget": maxTokens}).Info("antigravity budget: compacting oversized context")
	rawJSON, _ = CompactHistoricalToolOutputs(rawJSON, contentsPath)
	if EstimateGeminiPayloadTokens(rawJSON, contentsPath) <= maxTokens {
		return rawJSON
	}
	rawJSON = pruneOldestTurns(rawJSON, contentsPath, maxTokens)
	if EstimateGeminiPayloadTokens(rawJSON, contentsPath) > maxTokens {
		rawJSON, _ = compactToolOutputs(rawJSON, contentsPath, MaxActiveToolOutputLen, true)
	}
	if EstimateGeminiPayloadTokens(rawJSON, contentsPath) > maxTokens {
		rawJSON = compactOversizedMessageTexts(rawJSON, contentsPath, maxTokens)
	}
	currentTokens = EstimateGeminiPayloadTokens(rawJSON, contentsPath)
	log.WithFields(log.Fields{"model": modelName, "estimated_tokens": currentTokens, "budget": maxTokens}).Info("antigravity budget: compaction finished")
	return rawJSON
}
