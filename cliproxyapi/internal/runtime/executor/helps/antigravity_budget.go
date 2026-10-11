package helps

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	geminicommon "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/gemini/common"
	"github.com/tidwall/gjson"
)

// CompactAntigravityTokenLimitRetry prepares a single smaller retry only for
// Google's explicit input-token-limit rejection. It does not retry unrelated
// 400s, authentication failures, quota errors, or successful stream responses.
func CompactAntigravityTokenLimitRetry(status int, body []byte, request *http.Request, modelName string) ([]byte, bool) {
	if status != http.StatusBadRequest || !strings.HasPrefix(strings.ToLower(modelName), "gemini") {
		return nil, false
	}
	message := gjson.GetBytes(body, "error.message").String()
	if message == "" {
		message = string(body)
	}
	message = strings.ToLower(message)
	if !strings.Contains(message, "input token count") || !strings.Contains(message, "exceeds") ||
		!strings.Contains(message, "maximum number of tokens allowed") {
		return nil, false
	}
	// Retry from the body actually sent. Compacting an earlier translated copy
	// can otherwise rebuild the identical body already shortened by the guard.
	if request == nil || request.GetBody == nil {
		return nil, false
	}
	reader, err := request.GetBody()
	if err != nil {
		return nil, false
	}
	payload, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		return nil, false
	}
	path := geminicommon.ResolveContentsPath(payload, "request.contents")
	estimate := geminicommon.EstimateGeminiPayloadTokens(payload, path)
	if estimate <= 0 {
		return nil, false
	}
	// Force a real reduction even when the local heuristic originally put this
	// request below the normal budget. This is not an identical-body retry.
	limit := estimate / 2
	if limit > geminicommon.AntigravityDefaultMaxTokens/2 {
		limit = geminicommon.AntigravityDefaultMaxTokens / 2
	}
	compacted := geminicommon.EnsureAntigravityBudgetWithLimit(payload, path, modelName, limit)
	if bytes.Equal(compacted, payload) || geminicommon.EstimateGeminiPayloadTokens(compacted, path) > limit {
		return nil, false
	}
	return compacted, true
}
