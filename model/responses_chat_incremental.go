package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

const (
	responsesChatStorageModeLegacy      = "legacy"
	responsesChatStorageModeShadow      = "shadow"
	responsesChatStorageModeIncremental = "incremental"
	responsesChatCheckpointInterval     = int64(20)
	responsesChatReplayLimit            = 24
	responsesChatShadowLogEvery         = uint64(500)
	responsesChatShadowErrorLogEvery    = uint64(100)
	responsesChatSaveLockSlots          = 64
	responsesChatRebuildDefaultLimit    = 2
	responsesChatRebuildMaximumLimit    = 4
	responsesChatDeltaVersion           = 2
)

type ResponsesChatTurn struct {
	ResponseId           string `gorm:"primaryKey;size:64"`
	ConversationId       string `gorm:"size:64;index:idx_resp_turn_conv_depth,priority:1"`
	ParentResponseId     string `gorm:"size:64;index"`
	Depth                int64  `gorm:"index:idx_resp_turn_conv_depth,priority:2"`
	CheckpointResponseId string `gorm:"size:64;index"`
	UserId               int    `gorm:"index"`
	TokenId              int    `gorm:"index"`
	ModelName            string `gorm:"size:255"`
	ItemCount            int
	DeltaItems           string `gorm:"type:text"`
	PendingToolCalls     string `gorm:"type:text"`
	FullItemsHash        string `gorm:"size:64"`
	PendingHash          string `gorm:"size:64"`
	CreatedAt            int64  `gorm:"index"`
}

type ResponsesChatCheckpoint struct {
	ResponseId     string `gorm:"primaryKey;size:64"`
	ConversationId string `gorm:"size:64;index"`
	Depth          int64
	UserId         int    `gorm:"index"`
	TokenId        int    `gorm:"index"`
	Items          string `gorm:"type:text"`
	CreatedAt      int64  `gorm:"index"`
}

type ResponsesChatHead struct {
	ConversationId string `gorm:"primaryKey;size:64"`
	ResponseId     string `gorm:"size:64;index"`
	Depth          int64
	UserId         int `gorm:"index"`
	TokenId        int `gorm:"index"`
	CreatedAt      int64
	UpdatedAt      int64 `gorm:"index"`
}

type ResponsesChatStorageStats struct {
	Mode              string
	ShadowChecks      uint64
	ShadowMatches     uint64
	ShadowMismatches  uint64
	CacheHits         uint64
	CacheMisses       uint64
	CacheEvictions    uint64
	IncrementalReads  uint64
	LegacyFallbacks   uint64
	BlobErrors        uint64
	ShadowReadErrors  uint64
	ShadowWriteErrors uint64
	RebuildWaits      uint64
	RebuildActive     int
	RebuildLimit      int
}

type responsesChatPreparedTurn struct {
	turn       ResponsesChatTurn
	checkpoint *ResponsesChatCheckpoint
	fullItems  string
	pending    string
}

type responsesChatItemReplacement struct {
	Index int            `json:"index"`
	Item  map[string]any `json:"item"`
}

type responsesChatDelta struct {
	Version       int                            `json:"version"`
	BaseItemCount int                            `json:"base_item_count"`
	Replacements  []responsesChatItemReplacement `json:"replacements,omitempty"`
	Append        []map[string]any               `json:"append,omitempty"`
}

var (
	responsesChatReadGroup        singleflight.Group
	responsesChatSaveLocks        [responsesChatSaveLockSlots]sync.Mutex
	responsesChatShadowChecks     atomic.Uint64
	responsesChatShadowMatches    atomic.Uint64
	responsesChatShadowMismatch   atomic.Uint64
	responsesChatIncrementalRead  atomic.Uint64
	responsesChatLegacyFallback   atomic.Uint64
	responsesChatShadowReadError  atomic.Uint64
	responsesChatShadowWriteError atomic.Uint64
	responsesChatRebuildWaits     atomic.Uint64
	responsesChatRebuildMu        sync.Mutex
	responsesChatRebuildCond      = sync.NewCond(&responsesChatRebuildMu)
	responsesChatRebuildActive    int
)

func ResponsesChatStorageMode() string {
	mode := strings.ToLower(strings.TrimSpace(common.GetEnvOrDefaultString("RESPONSES_SESSION_STORAGE_MODE", responsesChatStorageModeShadow)))
	switch mode {
	case responsesChatStorageModeLegacy, responsesChatStorageModeIncremental:
		return mode
	default:
		return responsesChatStorageModeShadow
	}
}

func ResponsesChatStorageStatsSnapshot() ResponsesChatStorageStats {
	return ResponsesChatStorageStats{
		Mode:              ResponsesChatStorageMode(),
		ShadowChecks:      responsesChatShadowChecks.Load(),
		ShadowMatches:     responsesChatShadowMatches.Load(),
		ShadowMismatches:  responsesChatShadowMismatch.Load(),
		CacheHits:         responsesChatCacheHits.Load(),
		CacheMisses:       responsesChatCacheMisses.Load(),
		CacheEvictions:    responsesChatCacheEvictions.Load(),
		IncrementalReads:  responsesChatIncrementalRead.Load(),
		LegacyFallbacks:   responsesChatLegacyFallback.Load(),
		BlobErrors:        responsesChatBlobErrors.Load(),
		ShadowReadErrors:  responsesChatShadowReadError.Load(),
		ShadowWriteErrors: responsesChatShadowWriteError.Load(),
		RebuildWaits:      responsesChatRebuildWaits.Load(),
		RebuildActive:     responsesChatRebuildActiveCount(),
		RebuildLimit:      responsesChatRebuildConcurrency(),
	}
}

func responsesChatRebuildConcurrency() int {
	limit := common.GetEnvOrDefault("RESPONSES_SESSION_REBUILD_CONCURRENCY", responsesChatRebuildDefaultLimit)
	if limit < 1 {
		return 1
	}
	if limit > responsesChatRebuildMaximumLimit {
		return responsesChatRebuildMaximumLimit
	}
	return limit
}

func responsesChatRebuildActiveCount() int {
	responsesChatRebuildMu.Lock()
	defer responsesChatRebuildMu.Unlock()
	return responsesChatRebuildActive
}

func acquireResponsesChatRebuildSlot() func() {
	limit := responsesChatRebuildConcurrency()
	responsesChatRebuildMu.Lock()
	if responsesChatRebuildActive >= limit {
		responsesChatRebuildWaits.Add(1)
	}
	for responsesChatRebuildActive >= limit {
		responsesChatRebuildCond.Wait()
	}
	responsesChatRebuildActive++
	responsesChatRebuildMu.Unlock()
	return func() {
		responsesChatRebuildMu.Lock()
		responsesChatRebuildActive--
		responsesChatRebuildCond.Signal()
		responsesChatRebuildMu.Unlock()
	}
}

func responsesChatHash(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func canonicalResponsesChatItems(items []map[string]any) (string, error) {
	data, err := common.Marshal(items)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func canonicalResponsesChatPending(pending string) (string, error) {
	if strings.TrimSpace(pending) == "" {
		return "{}", nil
	}
	var value any
	if err := common.UnmarshalJsonStr(pending, &value); err != nil {
		return "", err
	}
	data, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func responsesChatItemsDelta(parentItems string, full []map[string]any) (string, bool, error) {
	var parent []map[string]any
	if strings.TrimSpace(parentItems) == "" {
		parent = nil
	} else if err := common.UnmarshalJsonStr(parentItems, &parent); err != nil {
		return "", false, err
	}
	if len(parent) > len(full) {
		return "", false, nil
	}
	replacements := make([]responsesChatItemReplacement, 0)
	for index := range parent {
		if !reflect.DeepEqual(parent[index], full[index]) {
			replacements = append(replacements, responsesChatItemReplacement{Index: index, Item: full[index]})
		}
	}
	delta, err := common.Marshal(responsesChatDelta{
		Version:       responsesChatDeltaVersion,
		BaseItemCount: len(parent),
		Replacements:  replacements,
		Append:        full[len(parent):],
	})
	if err != nil {
		return "", false, err
	}
	return string(delta), true, nil
}

func applyResponsesChatItemsDelta(current []map[string]any, payload string) ([]map[string]any, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" || payload == "[]" {
		return current, nil
	}
	if payload[0] == '[' {
		var legacyAppend []map[string]any
		if err := common.UnmarshalJsonStr(payload, &legacyAppend); err != nil {
			return nil, err
		}
		combined := make([]map[string]any, 0, len(current)+len(legacyAppend))
		combined = append(combined, current...)
		return append(combined, legacyAppend...), nil
	}
	if payload[0] != '{' {
		return nil, errors.New("responses session delta format is invalid")
	}
	var metadata struct {
		Version int `json:"version"`
	}
	if err := common.UnmarshalJsonStr(payload, &metadata); err != nil {
		return nil, err
	}
	if metadata.Version != responsesChatDeltaVersion {
		return nil, errors.New("responses session delta version is unsupported")
	}
	var delta responsesChatDelta
	if err := common.UnmarshalJsonStr(payload, &delta); err != nil {
		return nil, err
	}
	if delta.BaseItemCount != len(current) {
		return nil, errors.New("responses session delta base count mismatch")
	}
	next := make([]map[string]any, len(current), len(current)+len(delta.Append))
	copy(next, current)
	lastIndex := -1
	for _, replacement := range delta.Replacements {
		if replacement.Index <= lastIndex || replacement.Index >= delta.BaseItemCount {
			return nil, errors.New("responses session delta replacement index is invalid")
		}
		if replacement.Item == nil {
			return nil, errors.New("responses session delta replacement item is missing")
		}
		next[replacement.Index] = replacement.Item
		lastIndex = replacement.Index
	}
	return append(next, delta.Append...), nil
}

func lockResponsesChatConversation(conversationID string) func() {
	sum := sha256.Sum256([]byte(conversationID))
	slot := int(sum[0]) % len(responsesChatSaveLocks)
	responsesChatSaveLocks[slot].Lock()
	return responsesChatSaveLocks[slot].Unlock
}

func prepareResponsesChatTurn(session *ResponsesChatSession) (*responsesChatPreparedTurn, error) {
	var full []map[string]any
	if err := common.UnmarshalJsonStr(session.Items, &full); err != nil {
		return nil, fmt.Errorf("decode responses session items: %w", err)
	}
	fullItems, err := canonicalResponsesChatItems(full)
	if err != nil {
		return nil, err
	}
	if err := validateResponsesChatPayloadSize("responses session items", fullItems); err != nil {
		return nil, err
	}
	pending, err := canonicalResponsesChatPending(session.PendingToolCalls)
	if err != nil {
		return nil, fmt.Errorf("decode responses pending calls: %w", err)
	}
	if err := validateResponsesChatPayloadSize("responses session pending calls", pending); err != nil {
		return nil, err
	}

	parentID := strings.TrimSpace(session.ParentResponseId)
	var head ResponsesChatHead
	headErr := DB.Where("conversation_id = ?", session.ConversationId).First(&head).Error
	if headErr != nil && !errors.Is(headErr, gorm.ErrRecordNotFound) {
		return nil, headErr
	}
	if parentID == "" && headErr == nil && (head.UserId == 0 || session.UserId == 0 || head.UserId == session.UserId) {
		parentID = head.ResponseId
	}

	var parentTurn ResponsesChatTurn
	parentFound := false
	parentIsHead := false
	if parentID != "" {
		err := DB.Where("response_id = ?", parentID).First(&parentTurn).Error
		if err == nil && parentTurn.ConversationId == session.ConversationId &&
			(parentTurn.UserId == 0 || session.UserId == 0 || parentTurn.UserId == session.UserId) {
			parentFound = true
			parentIsHead = headErr == nil && head.ResponseId == parentID
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	depth := int64(1)
	if headErr == nil && (head.UserId == 0 || session.UserId == 0 || head.UserId == session.UserId) {
		depth = head.Depth + 1
	}
	checkpointID := ""
	deltaItems := fullItems
	forceCheckpoint := true
	if parentFound {
		depth = parentTurn.Depth + 1
		parentSession, err := GetResponsesChatSession(parentID)
		if err != nil {
			return nil, err
		}
		if parentSession != nil {
			checkpointID = parentTurn.CheckpointResponseId
			delta, deltaSupported, err := responsesChatItemsDelta(parentSession.Items, full)
			if err != nil {
				return nil, err
			}
			deltaItems = delta
			forceCheckpoint = !deltaSupported || !parentIsHead
			if !deltaSupported {
				checkpointID = ""
			}
		} else {
			forceCheckpoint = true
			checkpointID = ""
		}
	}
	if !parentFound || checkpointID == "" || depth%responsesChatCheckpointInterval == 0 {
		forceCheckpoint = true
	}

	now := common.GetTimestamp()
	turn := ResponsesChatTurn{
		ResponseId:           session.ID,
		ConversationId:       session.ConversationId,
		ParentResponseId:     parentID,
		Depth:                depth,
		CheckpointResponseId: checkpointID,
		UserId:               session.UserId,
		TokenId:              session.TokenId,
		ModelName:            session.ModelName,
		ItemCount:            len(full),
		DeltaItems:           deltaItems,
		PendingToolCalls:     pending,
		FullItemsHash:        responsesChatHash(fullItems),
		PendingHash:          responsesChatHash(pending),
		CreatedAt:            now,
	}
	prepared := &responsesChatPreparedTurn{
		turn:      turn,
		fullItems: fullItems,
		pending:   pending,
	}
	if forceCheckpoint {
		prepared.turn.CheckpointResponseId = session.ID
		prepared.turn.DeltaItems = "[]"
		prepared.checkpoint = &ResponsesChatCheckpoint{
			ResponseId:     session.ID,
			ConversationId: session.ConversationId,
			Depth:          depth,
			UserId:         session.UserId,
			TokenId:        session.TokenId,
			Items:          fullItems,
			CreatedAt:      now,
		}
	}
	return prepared, nil
}

func writePreparedResponsesChatTurn(tx *gorm.DB, prepared *responsesChatPreparedTurn) error {
	if prepared == nil {
		return nil
	}
	deltaPayload, err := externalizeResponsesChatPayload(prepared.turn.DeltaItems)
	if err != nil {
		return err
	}
	delta, err := encodeResponsesChatPayload(deltaPayload)
	if err != nil {
		return err
	}
	prepared.turn.DeltaItems = delta
	pendingPayload, err := externalizeResponsesChatPayload(prepared.turn.PendingToolCalls)
	if err != nil {
		return err
	}
	prepared.turn.PendingToolCalls, err = encodeResponsesChatPayload(pendingPayload)
	if err != nil {
		return err
	}
	if prepared.checkpoint != nil {
		itemsPayload, err := externalizeResponsesChatPayload(prepared.checkpoint.Items)
		if err != nil {
			return err
		}
		items, err := encodeResponsesChatPayload(itemsPayload)
		if err != nil {
			return err
		}
		prepared.checkpoint.Items = items
		if err := tx.Save(prepared.checkpoint).Error; err != nil {
			return err
		}
	}
	if err := tx.Save(&prepared.turn).Error; err != nil {
		return err
	}
	var head ResponsesChatHead
	err = lockForUpdate(tx).Where("conversation_id = ?", prepared.turn.ConversationId).First(&head).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		head = ResponsesChatHead{
			ConversationId: prepared.turn.ConversationId,
			ResponseId:     prepared.turn.ResponseId,
			Depth:          prepared.turn.Depth,
			UserId:         prepared.turn.UserId,
			TokenId:        prepared.turn.TokenId,
			CreatedAt:      prepared.turn.CreatedAt,
			UpdatedAt:      prepared.turn.CreatedAt,
		}
		return tx.Create(&head).Error
	}
	if err != nil {
		return err
	}
	return tx.Model(&head).Updates(map[string]any{
		"response_id": prepared.turn.ResponseId,
		"depth":       prepared.turn.Depth,
		"user_id":     prepared.turn.UserId,
		"token_id":    prepared.turn.TokenId,
		"updated_at":  prepared.turn.CreatedAt,
	}).Error
}

func loadIncrementalResponsesChatSessionWithDB(db *gorm.DB, id string) (*ResponsesChatSession, error) {
	release := acquireResponsesChatRebuildSlot()
	defer release()

	var turn ResponsesChatTurn
	if err := db.Where("response_id = ?", id).First(&turn).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var checkpoint ResponsesChatCheckpoint
	if err := db.Where("response_id = ?", turn.CheckpointResponseId).First(&checkpoint).Error; err != nil {
		return nil, fmt.Errorf("load responses session checkpoint: %w", err)
	}
	if checkpoint.ConversationId != turn.ConversationId || checkpoint.Depth > turn.Depth {
		return nil, errors.New("responses session checkpoint does not match turn")
	}
	checkpointItems, err := decodeResponsesChatPayload(checkpoint.Items)
	if err != nil {
		return nil, err
	}
	checkpointItems, err = hydrateResponsesChatPayload(checkpointItems)
	if err != nil {
		return nil, err
	}
	if err := validateResponsesChatPayloadSize("responses session checkpoint", checkpointItems); err != nil {
		return nil, err
	}
	var items []map[string]any
	if strings.TrimSpace(checkpointItems) != "" {
		if err := common.UnmarshalJsonStr(checkpointItems, &items); err != nil {
			return nil, err
		}
	}
	chain := make([]ResponsesChatTurn, 0, responsesChatCheckpointInterval)
	if turn.ResponseId != turn.CheckpointResponseId {
		var candidates []ResponsesChatTurn
		if err := db.Where(
			"conversation_id = ? AND depth > ? AND depth <= ?",
			turn.ConversationId,
			checkpoint.Depth,
			turn.Depth,
		).Find(&candidates).Error; err != nil {
			return nil, fmt.Errorf("load responses session incremental chain: %w", err)
		}
		byResponseID := make(map[string]ResponsesChatTurn, len(candidates))
		for _, candidate := range candidates {
			byResponseID[candidate.ResponseId] = candidate
		}
		current := turn
		for current.ResponseId != turn.CheckpointResponseId {
			if len(chain) >= responsesChatReplayLimit {
				return nil, errors.New("responses session replay exceeds turn limit")
			}
			chain = append(chain, current)
			if current.ParentResponseId == "" {
				return nil, errors.New("responses session replay chain is broken")
			}
			if current.ParentResponseId == turn.CheckpointResponseId {
				break
			}
			parent, ok := byResponseID[current.ParentResponseId]
			if !ok {
				return nil, errors.New("responses session replay chain is broken")
			}
			current = parent
		}
	}
	for left, right := 0, len(chain)-1; left < right; left, right = left+1, right-1 {
		chain[left], chain[right] = chain[right], chain[left]
	}
	for _, item := range chain {
		delta, err := decodeResponsesChatPayload(item.DeltaItems)
		if err != nil {
			return nil, err
		}
		delta, err = hydrateResponsesChatPayload(delta)
		if err != nil {
			return nil, err
		}
		items, err = applyResponsesChatItemsDelta(items, delta)
		if err != nil {
			return nil, err
		}
	}
	fullItems, err := canonicalResponsesChatItems(items)
	if err != nil {
		return nil, err
	}
	if err := validateResponsesChatPayloadSize("responses session items", fullItems); err != nil {
		return nil, err
	}
	if len(items) != turn.ItemCount || responsesChatHash(fullItems) != turn.FullItemsHash {
		return nil, errors.New("responses session replay hash mismatch")
	}
	pending, err := decodeResponsesChatPayload(turn.PendingToolCalls)
	if err != nil {
		return nil, err
	}
	pending, err = hydrateResponsesChatPayload(pending)
	if err != nil {
		return nil, err
	}
	if err := validateResponsesChatPayloadSize("responses session pending calls", pending); err != nil {
		return nil, err
	}
	if responsesChatHash(pending) != turn.PendingHash {
		return nil, errors.New("responses session pending hash mismatch")
	}
	return &ResponsesChatSession{
		ID:               turn.ResponseId,
		UserId:           turn.UserId,
		TokenId:          turn.TokenId,
		ConversationId:   turn.ConversationId,
		ParentResponseId: turn.ParentResponseId,
		ModelName:        turn.ModelName,
		Items:            fullItems,
		PendingToolCalls: pending,
		CreatedAt:        turn.CreatedAt,
		UpdatedAt:        turn.CreatedAt,
	}, nil
}

func responsesChatSessionsMatch(left, right *ResponsesChatSession) bool {
	if left == nil || right == nil {
		return false
	}
	leftItems, leftErr := canonicalResponsesChatItemsFromJSON(left.Items)
	rightItems, rightErr := canonicalResponsesChatItemsFromJSON(right.Items)
	leftPending, leftPendingErr := canonicalResponsesChatPending(left.PendingToolCalls)
	rightPending, rightPendingErr := canonicalResponsesChatPending(right.PendingToolCalls)
	return leftErr == nil && rightErr == nil && leftPendingErr == nil && rightPendingErr == nil &&
		responsesChatHash(leftItems) == responsesChatHash(rightItems) &&
		responsesChatHash(leftPending) == responsesChatHash(rightPending)
}

func canonicalResponsesChatItemsFromJSON(data string) (string, error) {
	var items []map[string]any
	if strings.TrimSpace(data) == "" {
		items = nil
	} else if err := common.UnmarshalJsonStr(data, &items); err != nil {
		return "", err
	}
	return canonicalResponsesChatItems(items)
}

func recordResponsesChatShadow(sessionID string, match bool) {
	checks := responsesChatShadowChecks.Add(1)
	if match {
		responsesChatShadowMatches.Add(1)
	} else {
		mismatches := responsesChatShadowMismatch.Add(1)
		if mismatches <= 5 || mismatches%responsesChatShadowErrorLogEvery == 0 {
			common.SysError(fmt.Sprintf("responses session shadow mismatch count=%d response_id=%s", mismatches, sessionID))
		}
	}
	if checks%responsesChatShadowLogEvery == 0 {
		common.SysLog(fmt.Sprintf("responses session shadow checks=%d matches=%d mismatches=%d",
			checks, responsesChatShadowMatches.Load(), responsesChatShadowMismatch.Load()))
	}
}

func recordResponsesChatShadowError(stage, sessionID string, err error) {
	var count uint64
	if stage == "read" {
		count = responsesChatShadowReadError.Add(1)
	} else {
		count = responsesChatShadowWriteError.Add(1)
	}
	if count <= 5 || count%responsesChatShadowErrorLogEvery == 0 {
		common.SysError(fmt.Sprintf("responses session shadow %s error count=%d response_id=%s: %v", stage, count, sessionID, err))
	}
}

func GetResponsesChatSession(id string) (*ResponsesChatSession, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	mode := ResponsesChatStorageMode()
	if mode == responsesChatStorageModeLegacy {
		return getLegacyResponsesChatSession(id)
	}
	if mode == responsesChatStorageModeIncremental {
		if session, ok := cachedResponsesChatSession(id); ok {
			return session, nil
		}
	}
	value, err, _ := responsesChatReadGroup.Do("read:"+mode+":"+id, func() (any, error) {
		if mode == responsesChatStorageModeShadow {
			incremental, incrementalErr := loadIncrementalResponsesChatSessionWithDB(DB, id)
			legacy, legacyErr := getLegacyResponsesChatSession(id)
			if incrementalErr != nil && legacyErr == nil {
				recordResponsesChatShadowError("read", id, incrementalErr)
			}
			if incrementalErr == nil && legacyErr == nil && incremental != nil && legacy != nil {
				var head ResponsesChatHead
				if headErr := DB.Where("conversation_id = ? AND response_id = ?", incremental.ConversationId, id).First(&head).Error; headErr == nil {
					recordResponsesChatShadow(id, responsesChatSessionsMatch(incremental, legacy))
				}
			}
			if legacyErr != nil {
				return nil, legacyErr
			}
			if legacy != nil {
				return legacy, nil
			}
			return incremental, incrementalErr
		}
		incremental, incrementalErr := loadIncrementalResponsesChatSessionWithDB(DB, id)
		if incrementalErr == nil && incremental != nil {
			responsesChatIncrementalRead.Add(1)
			cacheResponsesChatSession(incremental)
			return incremental, nil
		}
		legacy, legacyErr := getLegacyResponsesChatSession(id)
		if legacyErr != nil {
			return nil, legacyErr
		}
		if incrementalErr != nil && legacy == nil {
			return nil, incrementalErr
		}
		if legacy != nil {
			responsesChatLegacyFallback.Add(1)
			cacheResponsesChatSession(legacy)
		}
		return legacy, nil
	})
	if err != nil || value == nil {
		return nil, err
	}
	return value.(*ResponsesChatSession), nil
}

func SaveResponsesChatSession(session *ResponsesChatSession) error {
	if session == nil {
		return nil
	}
	if strings.TrimSpace(session.ID) == "" {
		return errors.New("responses chat session id is required")
	}
	mode := ResponsesChatStorageMode()
	if session.ConversationId == "" || mode == responsesChatStorageModeLegacy {
		return saveLegacyResponsesChatSessionWithDB(DB, session)
	}
	unlock := lockResponsesChatConversation(session.ConversationId)
	defer unlock()
	prepared, err := prepareResponsesChatTurn(session)
	if err != nil {
		if mode == responsesChatStorageModeShadow {
			if legacyErr := saveLegacyResponsesChatSessionWithDB(DB, session); legacyErr != nil {
				return legacyErr
			}
			recordResponsesChatShadowError("write", session.ID, err)
			return nil
		}
		return err
	}
	if mode == responsesChatStorageModeIncremental {
		if err := DB.Transaction(func(tx *gorm.DB) error {
			return writePreparedResponsesChatTurn(tx, prepared)
		}); err != nil {
			return err
		}
	} else {
		if err := saveLegacyResponsesChatSessionWithDB(DB, session); err != nil {
			return err
		}
		if err := DB.Transaction(func(tx *gorm.DB) error {
			return writePreparedResponsesChatTurn(tx, prepared)
		}); err != nil {
			recordResponsesChatShadowError("write", session.ID, err)
			return nil
		}
		replayed, err := loadIncrementalResponsesChatSessionWithDB(DB, session.ID)
		if err != nil {
			recordResponsesChatShadowError("write", session.ID, err)
			return nil
		}
		recordResponsesChatShadow(session.ID, responsesChatSessionsMatch(replayed, &ResponsesChatSession{
			ID: session.ID, Items: prepared.fullItems, PendingToolCalls: prepared.pending,
		}))
		return nil
	}
	if mode == responsesChatStorageModeIncremental {
		canonicalSession := *session
		canonicalSession.ParentResponseId = prepared.turn.ParentResponseId
		canonicalSession.Items = prepared.fullItems
		canonicalSession.PendingToolCalls = prepared.pending
		canonicalSession.CreatedAt = prepared.turn.CreatedAt
		canonicalSession.UpdatedAt = prepared.turn.CreatedAt
		cacheResponsesChatSession(&canonicalSession)
	}
	return nil
}

func CleanupResponsesChatSessions(olderThanSeconds int64, batchSize int) (int64, error) {
	if olderThanSeconds <= 0 {
		return 0, nil
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	cutoff := common.GetTimestamp() - olderThanSeconds
	total, err := cleanupLegacyResponsesChatSessions(olderThanSeconds, batchSize)
	if err != nil {
		return total, err
	}
	conversationIDs := make([]string, 0, batchSize)
	if err := DB.Model(&ResponsesChatHead{}).Where("updated_at < ?", cutoff).Order("updated_at asc").Limit(batchSize).Pluck("conversation_id", &conversationIDs).Error; err != nil {
		return total, err
	}
	for _, conversationID := range conversationIDs {
		unlock := lockResponsesChatConversation(conversationID)
		var deleted int64
		err := DB.Transaction(func(tx *gorm.DB) error {
			for _, table := range []any{&ResponsesChatTurn{}, &ResponsesChatCheckpoint{}} {
				result := tx.Where("conversation_id = ?", conversationID).Delete(table)
				deleted += result.RowsAffected
				if result.Error != nil {
					return result.Error
				}
			}
			legacy := tx.Where("conversation_id = ?", conversationID).Delete(&ResponsesChatSession{})
			deleted += legacy.RowsAffected
			if legacy.Error != nil {
				return legacy.Error
			}
			head := tx.Where("conversation_id = ?", conversationID).Delete(&ResponsesChatHead{})
			deleted += head.RowsAffected
			return head.Error
		})
		unlock()
		total += deleted
		if err != nil {
			return total, err
		}
	}
	blobRetention := int64(common.GetEnvOrDefault("RESPONSES_SESSION_BLOB_RETENTION_SECONDS", 30*24*60*60))
	if blobRetention > 0 {
		if err := cleanupResponsesChatBlobs(common.GetTimestamp() - blobRetention); err != nil {
			return total, err
		}
	}
	return total, nil
}
