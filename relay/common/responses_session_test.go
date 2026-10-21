package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesConversationIDScopesThreadToCredential(t *testing.T) {
	info := &RelayInfo{
		UserId:         12,
		TokenId:        34,
		TokenKey:       "secret-key-a",
		RequestHeaders: map[string]string{"Thread-Id": "thread-one", "Session-Id": "session-one"},
	}
	first := ResponsesConversationID(info)
	require.NotEmpty(t, first)
	require.LessOrEqual(t, len(first), 64)
	require.NotContains(t, first, "thread-one")

	info.RequestHeaders = map[string]string{"thread-id": "thread-one", "session-id": "different"}
	require.Equal(t, first, ResponsesConversationID(info))

	info.TokenKey = "secret-key-b"
	require.NotEqual(t, first, ResponsesConversationID(info))

	info.RequestHeaders = nil
	require.Empty(t, ResponsesConversationID(info))
}
