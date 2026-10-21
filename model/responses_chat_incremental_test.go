package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func responsesChatStateJSON(turns int) (string, []map[string]any) {
	items := make([]map[string]any, 0, turns*2)
	for turn := 1; turn <= turns; turn++ {
		items = append(items,
			map[string]any{"type": "message", "role": "user", "content": fmt.Sprintf("user-%d", turn)},
			map[string]any{"type": "message", "role": "assistant", "content": fmt.Sprintf("assistant-%d", turn)},
		)
	}
	data, _ := common.Marshal(items)
	return string(data), items
}

func TestIncrementalResponsesChatCheckpointAndBranch(t *testing.T) {
	db := setupResponsesChatSessionTestDB(t)
	t.Setenv("RESPONSES_SESSION_STORAGE_MODE", responsesChatStorageModeIncremental)
	clearResponsesChatSessionCacheForTest()
	t.Cleanup(clearResponsesChatSessionCacheForTest)
	conversationID := "conv_incremental_checkpoint"
	previousID := ""
	for turn := 1; turn <= 25; turn++ {
		items, _ := responsesChatStateJSON(turn)
		id := fmt.Sprintf("resp_incremental_%02d", turn)
		require.NoError(t, SaveResponsesChatSession(&ResponsesChatSession{
			ID:               id,
			UserId:           41,
			TokenId:          42,
			ConversationId:   conversationID,
			ParentResponseId: previousID,
			ModelName:        "deepseek-flash",
			Items:            items,
			PendingToolCalls: `{}`,
		}))
		previousID = id
	}

	var turnCount, checkpointCount int64
	require.NoError(t, db.Model(&ResponsesChatTurn{}).Where("conversation_id = ?", conversationID).Count(&turnCount).Error)
	require.NoError(t, db.Model(&ResponsesChatCheckpoint{}).Where("conversation_id = ?", conversationID).Count(&checkpointCount).Error)
	require.EqualValues(t, 25, turnCount)
	require.EqualValues(t, 2, checkpointCount)

	var secondTurn ResponsesChatTurn
	require.NoError(t, db.Where("response_id = ?", "resp_incremental_02").First(&secondTurn).Error)
	var deltaItems []map[string]any
	delta, err := decodeResponsesChatPayload(secondTurn.DeltaItems)
	require.NoError(t, err)
	require.NoError(t, common.UnmarshalJsonStr(delta, &deltaItems))
	require.Len(t, deltaItems, 2)

	clearResponsesChatSessionCacheForTest()
	ten, err := GetResponsesChatSession("resp_incremental_10")
	require.NoError(t, err)
	expectedTen, _ := responsesChatStateJSON(10)
	require.Equal(t, expectedTen, ten.Items)

	clearResponsesChatSessionCacheForTest()
	twentyFive, err := GetResponsesChatSession("resp_incremental_25")
	require.NoError(t, err)
	expectedTwentyFive, _ := responsesChatStateJSON(25)
	require.Equal(t, expectedTwentyFive, twentyFive.Items)

	expectedBranch, baseItems := responsesChatStateJSON(10)
	_ = expectedBranch
	branchItems := append(baseItems,
		map[string]any{"type": "message", "role": "user", "content": "branch-user"},
		map[string]any{"type": "message", "role": "assistant", "content": "branch-assistant"},
	)
	branchJSON, err := common.Marshal(branchItems)
	require.NoError(t, err)
	require.NoError(t, SaveResponsesChatSession(&ResponsesChatSession{
		ID:               "resp_incremental_branch",
		UserId:           41,
		TokenId:          42,
		ConversationId:   conversationID,
		ParentResponseId: "resp_incremental_10",
		ModelName:        "deepseek-flash",
		Items:            string(branchJSON),
		PendingToolCalls: `{}`,
	}))
	var branchTurn ResponsesChatTurn
	require.NoError(t, db.Where("response_id = ?", "resp_incremental_branch").First(&branchTurn).Error)
	require.Equal(t, "resp_incremental_branch", branchTurn.CheckpointResponseId)
	clearResponsesChatSessionCacheForTest()
	branch, err := GetResponsesChatSession("resp_incremental_branch")
	require.NoError(t, err)
	require.Equal(t, string(branchJSON), branch.Items)
}

func TestResponsesChatShadowDetectsLegacyMismatch(t *testing.T) {
	_ = setupResponsesChatSessionTestDB(t)
	t.Setenv("RESPONSES_SESSION_STORAGE_MODE", responsesChatStorageModeShadow)
	t.Cleanup(clearResponsesChatSessionCacheForTest)
	conversationID := "conv_shadow_mismatch"
	items, _ := responsesChatStateJSON(1)
	require.NoError(t, SaveResponsesChatSession(&ResponsesChatSession{
		ID:               "resp_shadow_mismatch",
		UserId:           51,
		TokenId:          52,
		ConversationId:   conversationID,
		ModelName:        "deepseek-flash",
		Items:            items,
		PendingToolCalls: `{}`,
	}))
	before := ResponsesChatStorageStatsSnapshot().ShadowMismatches
	altered := `[{"type":"message","role":"assistant","content":"legacy-only"}]`
	require.NoError(t, saveLegacyResponsesChatSessionWithDB(DB, &ResponsesChatSession{
		ID:               "resp_shadow_mismatch",
		UserId:           51,
		TokenId:          52,
		ConversationId:   conversationID,
		ModelName:        "deepseek-flash",
		Items:            altered,
		PendingToolCalls: `{}`,
	}))
	session, err := GetResponsesChatSession("resp_shadow_mismatch")
	require.NoError(t, err)
	require.Equal(t, altered, session.Items)
	require.Greater(t, ResponsesChatStorageStatsSnapshot().ShadowMismatches, before)
}

func TestResponsesChatCacheHonorsByteLimit(t *testing.T) {
	cache := newResponsesChatSessionCache(300)
	cache.put(&ResponsesChatSession{ID: "one", Items: strings.Repeat("a", 100)})
	cache.put(&ResponsesChatSession{ID: "two", Items: strings.Repeat("b", 100)})
	_, foundOne := cache.get("one")
	require.False(t, foundOne)
	found, ok := cache.get("two")
	require.True(t, ok)
	require.Equal(t, "two", found.ID)

	oversized := &ResponsesChatSession{ID: "oversized", Items: strings.Repeat("c", 400)}
	cache.put(oversized)
	_, ok = cache.get("oversized")
	require.False(t, ok)
}

func TestResponsesChatCacheExpiresRetainedSession(t *testing.T) {
	t.Setenv("RESPONSES_SESSION_RETENTION_SECONDS", "10")
	cache := newResponsesChatSessionCache(1 << 20)
	cache.put(&ResponsesChatSession{
		ID:        "expired",
		Items:     `[]`,
		UpdatedAt: common.GetTimestamp() - 11,
	})
	_, ok := cache.get("expired")
	require.False(t, ok)
}

func TestResponsesChatBlobExternalizationRoundTrip(t *testing.T) {
	blobDir := t.TempDir()
	t.Setenv("RESPONSES_SESSION_BLOB_DIR", blobDir)
	t.Setenv("RESPONSES_SESSION_BLOB_THRESHOLD_BYTES", "1024")
	large := strings.Repeat("image-data-", 1024)
	items := []map[string]any{{
		"type": "message",
		"role": "user",
		"content": []any{map[string]any{
			"type":      "input_image",
			"image_url": "data:image/png;base64," + large,
		}},
	}}
	original, err := common.Marshal(items)
	require.NoError(t, err)
	externalized, err := externalizeResponsesChatPayload(string(original))
	require.NoError(t, err)
	require.Contains(t, externalized, "cmsg_blob_v1:")
	require.NotContains(t, externalized, large)
	hydrated, err := hydrateResponsesChatPayload(externalized)
	require.NoError(t, err)
	canonical, err := canonicalResponsesChatItemsFromJSON(hydrated)
	require.NoError(t, err)
	expected, err := canonicalResponsesChatItemsFromJSON(string(original))
	require.NoError(t, err)
	require.Equal(t, expected, canonical)

	files, err := os.ReadDir(blobDir)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.NoError(t, os.Chtimes(filepath.Join(blobDir, files[0].Name()), time.Unix(1, 0), time.Unix(1, 0)))
	require.NoError(t, cleanupResponsesChatBlobs(10))
	files, err = os.ReadDir(blobDir)
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestResponsesChatBlobPersistsThroughIncrementalStore(t *testing.T) {
	db := setupResponsesChatSessionTestDB(t)
	blobDir := t.TempDir()
	t.Setenv("RESPONSES_SESSION_STORAGE_MODE", responsesChatStorageModeIncremental)
	t.Setenv("RESPONSES_SESSION_BLOB_DIR", blobDir)
	t.Setenv("RESPONSES_SESSION_BLOB_THRESHOLD_BYTES", "1024")
	clearResponsesChatSessionCacheForTest()
	t.Cleanup(clearResponsesChatSessionCacheForTest)
	large := strings.Repeat("binary-image-", 2048)
	items := []map[string]any{{
		"type": "message",
		"role": "user",
		"content": []any{map[string]any{
			"type":      "input_image",
			"image_url": "data:image/png;base64," + large,
		}},
	}}
	itemsJSON, err := common.Marshal(items)
	require.NoError(t, err)
	require.NoError(t, SaveResponsesChatSession(&ResponsesChatSession{
		ID:               "resp_blob_incremental",
		UserId:           61,
		TokenId:          62,
		ConversationId:   "conv_blob_incremental",
		ModelName:        "deepseek-flash",
		Items:            string(itemsJSON),
		PendingToolCalls: `{}`,
	}))
	var checkpoint ResponsesChatCheckpoint
	require.NoError(t, db.Where("response_id = ?", "resp_blob_incremental").First(&checkpoint).Error)
	require.NotContains(t, checkpoint.Items, large)
	clearResponsesChatSessionCacheForTest()
	loaded, err := GetResponsesChatSession("resp_blob_incremental")
	require.NoError(t, err)
	expected, err := canonicalResponsesChatItemsFromJSON(string(itemsJSON))
	require.NoError(t, err)
	actual, err := canonicalResponsesChatItemsFromJSON(loaded.Items)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	files, err := os.ReadDir(blobDir)
	require.NoError(t, err)
	require.Len(t, files, 1)
}

func TestResponsesChatShadowCorruptionDoesNotRejectSave(t *testing.T) {
	db := setupResponsesChatSessionTestDB(t)
	t.Setenv("RESPONSES_SESSION_STORAGE_MODE", responsesChatStorageModeShadow)
	t.Cleanup(clearResponsesChatSessionCacheForTest)
	items, _ := responsesChatStateJSON(1)
	responseID := "resp_shadow_validation_mismatch"
	require.NoError(t, db.Exec(`CREATE TRIGGER corrupt_responses_checkpoint
		AFTER INSERT ON responses_chat_checkpoints
		WHEN NEW.response_id = '`+responseID+`'
		BEGIN
			UPDATE responses_chat_checkpoints SET items = '[]' WHERE response_id = NEW.response_id;
		END`).Error)
	t.Cleanup(func() {
		_ = db.Exec("DROP TRIGGER IF EXISTS corrupt_responses_checkpoint").Error
	})
	before := ResponsesChatStorageStatsSnapshot().ShadowWriteErrors
	require.NoError(t, SaveResponsesChatSession(&ResponsesChatSession{
		ID:               responseID,
		UserId:           71,
		TokenId:          72,
		ConversationId:   "conv_shadow_validation_mismatch",
		ModelName:        "deepseek-flash",
		Items:            items,
		PendingToolCalls: `{}`,
	}))
	require.Greater(t, ResponsesChatStorageStatsSnapshot().ShadowWriteErrors, before)

	loaded, err := GetResponsesChatSession(responseID)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Equal(t, items, loaded.Items)
}

func TestResponsesChatShadowWriteErrorKeepsLegacyPath(t *testing.T) {
	_ = setupResponsesChatSessionTestDB(t)
	t.Setenv("RESPONSES_SESSION_STORAGE_MODE", responsesChatStorageModeShadow)
	clearResponsesChatSessionCacheForTest()
	t.Cleanup(clearResponsesChatSessionCacheForTest)
	items, _ := responsesChatStateJSON(1)
	before := ResponsesChatStorageStatsSnapshot().ShadowWriteErrors
	require.NoError(t, SaveResponsesChatSession(&ResponsesChatSession{
		ID:               "resp_shadow_write_error",
		UserId:           73,
		TokenId:          74,
		ConversationId:   "conv_shadow_write_error",
		ModelName:        "deepseek-flash",
		Items:            items,
		PendingToolCalls: `not-json`,
	}))
	require.Greater(t, ResponsesChatStorageStatsSnapshot().ShadowWriteErrors, before)
	loaded, err := GetResponsesChatSession("resp_shadow_write_error")
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Equal(t, items, loaded.Items)
	require.Equal(t, `not-json`, loaded.PendingToolCalls)
}

func TestResponsesChatPayloadSizeLimitAppliesAfterDecompression(t *testing.T) {
	payload := strings.Repeat("a", (1<<20)+1)
	t.Setenv("RESPONSES_SESSION_MAX_BYTES", "2097152")
	encoded, err := encodeResponsesChatPayload(payload)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(encoded, responsesChatPayloadPrefix))

	t.Setenv("RESPONSES_SESSION_MAX_BYTES", "1048576")
	_, err = decodeResponsesChatPayload(encoded)
	require.ErrorContains(t, err, "size limit")
}

func TestResponsesChatRebuildConcurrencyLimit(t *testing.T) {
	t.Setenv("RESPONSES_SESSION_REBUILD_CONCURRENCY", "1")
	before := ResponsesChatStorageStatsSnapshot().RebuildWaits
	releaseFirst := acquireResponsesChatRebuildSlot()
	started := make(chan struct{})
	secondSlot := make(chan func(), 1)
	go func() {
		release := acquireResponsesChatRebuildSlot()
		close(started)
		secondSlot <- release
	}()

	select {
	case <-started:
		t.Fatal("second rebuild acquired the only slot")
	case <-time.After(50 * time.Millisecond):
	}
	releaseFirst()

	select {
	case releaseSecond := <-secondSlot:
		releaseSecond()
	case <-time.After(time.Second):
		t.Fatal("second rebuild did not resume")
	}
	require.Greater(t, ResponsesChatStorageStatsSnapshot().RebuildWaits, before)
	require.Zero(t, responsesChatRebuildActiveCount())
}

func TestResponsesChatCleanupKeepsActiveThreadAndLegacySnapshot(t *testing.T) {
	db := setupResponsesChatSessionTestDB(t)
	t.Setenv("RESPONSES_SESSION_STORAGE_MODE", responsesChatStorageModeShadow)
	clearResponsesChatSessionCacheForTest()
	t.Cleanup(clearResponsesChatSessionCacheForTest)
	conversationID := "conv_cleanup_active"
	items, _ := responsesChatStateJSON(1)
	require.NoError(t, SaveResponsesChatSession(&ResponsesChatSession{
		ID:               "resp_cleanup_active",
		UserId:           75,
		TokenId:          76,
		ConversationId:   conversationID,
		ModelName:        "deepseek-flash",
		Items:            items,
		PendingToolCalls: `{}`,
	}))
	old := common.GetTimestamp() - 100
	for _, table := range []any{&ResponsesChatSession{}, &ResponsesChatTurn{}, &ResponsesChatCheckpoint{}} {
		require.NoError(t, db.Model(table).Where("conversation_id = ?", conversationID).UpdateColumn("created_at", old).Error)
	}
	_, err := CleanupResponsesChatSessions(10, 10)
	require.NoError(t, err)
	for _, table := range []any{&ResponsesChatSession{}, &ResponsesChatTurn{}, &ResponsesChatCheckpoint{}, &ResponsesChatHead{}} {
		var count int64
		require.NoError(t, db.Model(table).Where("conversation_id = ?", conversationID).Count(&count).Error)
		require.NotZero(t, count)
	}

	require.NoError(t, db.Model(&ResponsesChatHead{}).Where("conversation_id = ?", conversationID).UpdateColumn("updated_at", old).Error)
	_, err = CleanupResponsesChatSessions(10, 10)
	require.NoError(t, err)
	for _, table := range []any{&ResponsesChatSession{}, &ResponsesChatTurn{}, &ResponsesChatCheckpoint{}, &ResponsesChatHead{}} {
		var count int64
		require.NoError(t, db.Model(table).Where("conversation_id = ?", conversationID).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestResponsesChatBlobFailureFallsBackInline(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0600))
	t.Setenv("RESPONSES_SESSION_BLOB_DIR", blocker)
	t.Setenv("RESPONSES_SESSION_BLOB_THRESHOLD_BYTES", "1024")
	large := strings.Repeat("inline-fallback-", 256)
	payload, err := common.Marshal([]map[string]any{{"type": "message", "content": large}})
	require.NoError(t, err)
	before := ResponsesChatStorageStatsSnapshot().BlobErrors
	externalized, err := externalizeResponsesChatPayload(string(payload))
	require.NoError(t, err)
	require.NotContains(t, externalized, "cmsg_blob_v1:")
	require.Contains(t, externalized, large)
	require.Greater(t, ResponsesChatStorageStatsSnapshot().BlobErrors, before)
}

func TestResponsesChatDatabaseTransactionFailureRollsBackTurn(t *testing.T) {
	db := setupResponsesChatSessionTestDB(t)
	t.Setenv("RESPONSES_SESSION_STORAGE_MODE", responsesChatStorageModeIncremental)
	clearResponsesChatSessionCacheForTest()
	t.Cleanup(clearResponsesChatSessionCacheForTest)
	require.NoError(t, db.Exec(`CREATE TRIGGER fail_responses_head
		BEFORE INSERT ON responses_chat_heads
		BEGIN
			SELECT RAISE(ABORT, 'forced head failure');
		END`).Error)
	t.Cleanup(func() {
		_ = db.Exec("DROP TRIGGER IF EXISTS fail_responses_head").Error
	})
	items, _ := responsesChatStateJSON(1)
	err := SaveResponsesChatSession(&ResponsesChatSession{
		ID:               "resp_forced_transaction_failure",
		UserId:           77,
		TokenId:          78,
		ConversationId:   "conv_forced_transaction_failure",
		ModelName:        "deepseek-flash",
		Items:            items,
		PendingToolCalls: `{}`,
	})
	require.ErrorContains(t, err, "forced head failure")
	var turnCount, checkpointCount int64
	require.NoError(t, db.Model(&ResponsesChatTurn{}).Where("response_id = ?", "resp_forced_transaction_failure").Count(&turnCount).Error)
	require.NoError(t, db.Model(&ResponsesChatCheckpoint{}).Where("response_id = ?", "resp_forced_transaction_failure").Count(&checkpointCount).Error)
	require.Zero(t, turnCount)
	require.Zero(t, checkpointCount)
}
