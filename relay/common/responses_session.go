package common

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// ResponsesConversationID returns a stable, non-reversible identity for a
// client conversation. Thread identity is scoped to the authenticated
// user/token so the same client-supplied header cannot cross account bounds.
func ResponsesConversationID(info *RelayInfo) string {
	if info == nil || info.UserId <= 0 {
		return ""
	}
	threadID := ""
	for _, name := range []string{"thread-id", "x-codex-thread-id", "session-id", "session_id"} {
		for key, value := range info.RequestHeaders {
			if strings.EqualFold(key, name) {
				threadID = strings.TrimSpace(value)
				break
			}
		}
		if threadID != "" {
			break
		}
	}
	if threadID == "" {
		return ""
	}
	credential := sha256.Sum256([]byte(info.TokenKey))
	scope := fmt.Sprintf("%d\x00%d\x00%x\x00%s", info.UserId, info.TokenId, credential, threadID)
	sum := sha256.Sum256([]byte(scope))
	return "conv_" + hex.EncodeToString(sum[:28])
}
