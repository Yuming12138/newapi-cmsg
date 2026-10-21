package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupResponsesChatSessionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&ResponsesChatSession{}, &ResponsesChatTurn{}, &ResponsesChatCheckpoint{}, &ResponsesChatHead{}))
	t.Cleanup(func() {
		DB.Exec("DELETE FROM responses_chat_sessions")
		DB.Exec("DELETE FROM responses_chat_turns")
		DB.Exec("DELETE FROM responses_chat_checkpoints")
		DB.Exec("DELETE FROM responses_chat_heads")
	})
	return DB
}

func TestResponsesChatPayloadRoundTrip(t *testing.T) {
	payload := strings.Repeat(`{"type":"message","content":"repeated transcript"}`, 5000)
	encoded, err := encodeResponsesChatPayload(payload)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(encoded, responsesChatPayloadPrefix))
	require.Less(t, len(encoded), len(payload))

	decoded, err := decodeResponsesChatPayload(encoded)
	require.NoError(t, err)
	require.Equal(t, payload, decoded)

	legacy, err := decodeResponsesChatPayload(`[{"type":"message"}]`)
	require.NoError(t, err)
	require.Equal(t, `[{"type":"message"}]`, legacy)
}

func TestResponsesChatSessionCanonicalHistory(t *testing.T) {
	db := setupResponsesChatSessionTestDB(t)
	conversationID := "conv_storage_test"
	firstItems := `[{"type":"message","role":"user","content":"` + strings.Repeat("a", 32*1024) + `"}]`
	secondItems := `[{"type":"message","role":"user","content":"second turn"},{"type":"message","role":"assistant","content":"answer"}]`

	require.NoError(t, SaveResponsesChatSession(&ResponsesChatSession{
		ID:               "resp_one",
		UserId:           7,
		TokenId:          9,
		ConversationId:   conversationID,
		ModelName:        "deepseek",
		Items:            firstItems,
		PendingToolCalls: `{}`,
	}))
	require.NoError(t, SaveResponsesChatSession(&ResponsesChatSession{
		ID:               "resp_two",
		UserId:           7,
		TokenId:          9,
		ConversationId:   conversationID,
		ModelName:        "mimo",
		Items:            secondItems,
		PendingToolCalls: `{"call_1":{"call_id":"call_1"}}`,
	}))

	var mappings []ResponsesChatSession
	require.NoError(t, db.Where("id IN ?", []string{"resp_one", "resp_two"}).Find(&mappings).Error)
	require.Len(t, mappings, 2)
	for _, mapping := range mappings {
		require.Empty(t, mapping.Items)
		require.Empty(t, mapping.PendingToolCalls)
	}

	var canonical ResponsesChatSession
	require.NoError(t, db.Where("id = ?", canonicalResponsesChatSessionID(conversationID)).First(&canonical).Error)
	var canonicalCount int64
	require.NoError(t, db.Model(&ResponsesChatSession{}).Where("id = ?", canonical.ID).Count(&canonicalCount).Error)
	require.EqualValues(t, 1, canonicalCount)

	session, err := GetResponsesChatSession("resp_one")
	require.NoError(t, err)
	require.NotNil(t, session)
	require.Equal(t, secondItems, session.Items)
	require.JSONEq(t, `{"call_1":{"call_id":"call_1"}}`, session.PendingToolCalls)
	require.Equal(t, 7, session.UserId)
}

func TestResponsesChatSessionLegacyRowAndCleanup(t *testing.T) {
	db := setupResponsesChatSessionTestDB(t)
	t.Setenv("RESPONSES_SESSION_STORAGE_MODE", responsesChatStorageModeLegacy)
	require.NoError(t, SaveResponsesChatSession(&ResponsesChatSession{
		ID:        "resp_legacy",
		UserId:    3,
		Items:     `[{"type":"message"}]`,
		ModelName: "legacy",
	}))
	legacy, err := GetResponsesChatSession("resp_legacy")
	require.NoError(t, err)
	require.Equal(t, `[{"type":"message"}]`, legacy.Items)

	require.NoError(t, SaveResponsesChatSession(&ResponsesChatSession{
		ID:             "resp_expired",
		UserId:         3,
		ConversationId: "conv_expired",
		Items:          `[]`,
	}))
	require.NoError(t, db.Model(&ResponsesChatSession{}).Where("conversation_id = ? OR id = ?", "conv_expired", "resp_expired").Update("created_at", common.GetTimestamp()-100).Error)
	deleted, err := CleanupResponsesChatSessions(10, 10)
	require.NoError(t, err)
	require.EqualValues(t, 2, deleted)

	var remaining int64
	require.NoError(t, db.Model(&ResponsesChatSession{}).Where("id = ?", "resp_expired").Count(&remaining).Error)
	require.Zero(t, remaining)
}

func TestGORMLoggerNeverIncludesBoundSessionParameters(t *testing.T) {
	config := newGORMLoggerConfig()
	require.True(t, config.ParameterizedQueries)
	require.True(t, config.IgnoreRecordNotFoundError)
	require.NotZero(t, config.SlowThreshold)
}
