package model

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	responsesChatPayloadPrefix           = "gz1:"
	responsesChatPayloadDefaultMaxBytes  = 128 << 20
	responsesChatPayloadMinimumMaxBytes  = 1 << 20
	responsesChatPayloadAbsoluteMaxBytes = 1 << 30
	responsesChatCleanupBatchMax         = 1000
)

type ResponsesChatSession struct {
	ID               string `gorm:"primaryKey;size:64"`
	UserId           int    `gorm:"index"`
	TokenId          int    `gorm:"index"`
	ConversationId   string `gorm:"size:64;index"`
	ParentResponseId string `gorm:"size:64;index"`
	ModelName        string `gorm:"size:255"`
	Items            string `gorm:"type:text"`
	PendingToolCalls string `gorm:"type:text"`
	CreatedAt        int64  `gorm:"index"`
	UpdatedAt        int64  `gorm:"index"`
}

func canonicalResponsesChatSessionID(conversationID string) string {
	sum := sha256.Sum256([]byte(conversationID))
	return "history_" + hex.EncodeToString(sum[:28])
}

func responsesChatMaxPayloadBytes() int {
	maxBytes := common.GetEnvOrDefault("RESPONSES_SESSION_MAX_BYTES", responsesChatPayloadDefaultMaxBytes)
	if maxBytes < responsesChatPayloadMinimumMaxBytes {
		return responsesChatPayloadMinimumMaxBytes
	}
	if maxBytes > responsesChatPayloadAbsoluteMaxBytes {
		return responsesChatPayloadAbsoluteMaxBytes
	}
	return maxBytes
}

func validateResponsesChatPayloadSize(label, payload string) error {
	if len(payload) > responsesChatMaxPayloadBytes() {
		return fmt.Errorf("%s exceeds responses session size limit", label)
	}
	return nil
}

func encodeResponsesChatPayload(payload string) (string, error) {
	if payload == "" {
		return "", nil
	}
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if err != nil {
		return "", err
	}
	if _, err := writer.Write([]byte(payload)); err != nil {
		_ = writer.Close()
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	encoded := responsesChatPayloadPrefix + base64.RawStdEncoding.EncodeToString(compressed.Bytes())
	if len(encoded) >= len(payload) {
		return payload, nil
	}
	return encoded, nil
}

func decodeResponsesChatPayload(payload string) (string, error) {
	maxBytes := responsesChatMaxPayloadBytes()
	if len(payload) > maxBytes {
		return "", errors.New("encoded session exceeds size limit")
	}
	if !strings.HasPrefix(payload, responsesChatPayloadPrefix) {
		return payload, nil
	}
	compressed, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(payload, responsesChatPayloadPrefix))
	if err != nil {
		return "", fmt.Errorf("decode compressed session: %w", err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return "", fmt.Errorf("open compressed session: %w", err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(io.LimitReader(reader, int64(maxBytes)+1))
	if err != nil {
		return "", fmt.Errorf("read compressed session: %w", err)
	}
	if len(decoded) > maxBytes {
		return "", errors.New("decoded session exceeds size limit")
	}
	return string(decoded), nil
}

func getLegacyResponsesChatSessionWithDB(db *gorm.DB, id string) (*ResponsesChatSession, error) {
	if id == "" {
		return nil, nil
	}
	var session ResponsesChatSession
	if err := db.Where("id = ?", id).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if session.ConversationId != "" {
		var canonical ResponsesChatSession
		canonicalID := canonicalResponsesChatSessionID(session.ConversationId)
		if err := db.Where("id = ?", canonicalID).First(&canonical).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil
			}
			return nil, err
		}
		if canonical.UserId != 0 && session.UserId != 0 && canonical.UserId != session.UserId {
			return nil, nil
		}
		session.Items = canonical.Items
		session.PendingToolCalls = canonical.PendingToolCalls
	}
	items, err := decodeResponsesChatPayload(session.Items)
	if err != nil {
		return nil, err
	}
	pending, err := decodeResponsesChatPayload(session.PendingToolCalls)
	if err != nil {
		return nil, err
	}
	items, err = hydrateResponsesChatPayload(items)
	if err != nil {
		return nil, err
	}
	pending, err = hydrateResponsesChatPayload(pending)
	if err != nil {
		return nil, err
	}
	session.Items = items
	session.PendingToolCalls = pending
	return &session, nil
}

func getLegacyResponsesChatSession(id string) (*ResponsesChatSession, error) {
	return getLegacyResponsesChatSessionWithDB(DB, id)
}

func saveLegacyResponsesChatSessionWithDB(db *gorm.DB, session *ResponsesChatSession) error {
	if session == nil {
		return nil
	}
	if strings.TrimSpace(session.ID) == "" {
		return errors.New("responses chat session id is required")
	}
	now := common.GetTimestamp()
	if session.CreatedAt == 0 {
		session.CreatedAt = now
	}
	session.UpdatedAt = now
	stored := *session
	var err error
	stored.Items, err = externalizeResponsesChatPayload(session.Items)
	if err != nil {
		return err
	}
	stored.PendingToolCalls, err = externalizeResponsesChatPayload(session.PendingToolCalls)
	if err != nil {
		return err
	}
	if err := validateResponsesChatPayloadSize("responses session items", stored.Items); err != nil {
		return err
	}
	if err := validateResponsesChatPayloadSize("responses session pending calls", stored.PendingToolCalls); err != nil {
		return err
	}

	// Preserve the legacy one-row format for callers without a stable thread ID.
	if session.ConversationId == "" {
		return db.Save(&stored).Error
	}

	items, err := encodeResponsesChatPayload(stored.Items)
	if err != nil {
		return err
	}
	pending, err := encodeResponsesChatPayload(stored.PendingToolCalls)
	if err != nil {
		return err
	}

	mapping := stored
	mapping.Items = ""
	mapping.PendingToolCalls = ""
	mapping.CreatedAt = now
	mapping.UpdatedAt = now

	canonical := stored
	canonical.ID = canonicalResponsesChatSessionID(session.ConversationId)
	canonical.Items = items
	canonical.PendingToolCalls = pending
	canonical.CreatedAt = now
	canonical.UpdatedAt = now

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&mapping).Error; err != nil {
			return err
		}
		return tx.Save(&canonical).Error
	})
}

func cleanupLegacyResponsesChatSessions(olderThanSeconds int64, batchSize int) (int64, error) {
	if olderThanSeconds <= 0 {
		return 0, nil
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	if batchSize > responsesChatCleanupBatchMax {
		batchSize = responsesChatCleanupBatchMax
	}
	cutoff := common.GetTimestamp() - olderThanSeconds
	ids := make([]string, 0, batchSize)
	headConversationIDs := DB.Model(&ResponsesChatHead{}).Select("conversation_id")
	if err := DB.Model(&ResponsesChatSession{}).
		Where("created_at < ? AND (conversation_id = '' OR conversation_id NOT IN (?))", cutoff, headConversationIDs).
		Order("created_at asc").
		Limit(batchSize).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result := DB.Where("id IN ? AND (conversation_id = '' OR conversation_id NOT IN (?))", ids, headConversationIDs).
		Delete(&ResponsesChatSession{})
	return result.RowsAffected, result.Error
}
