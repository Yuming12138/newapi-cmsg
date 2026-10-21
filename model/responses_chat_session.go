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
	responsesChatPayloadPrefix     = "gz1:"
	responsesChatPayloadMaxDecoded = 128 << 20
	responsesChatCleanupBatchMax   = 1000
)

type ResponsesChatSession struct {
	ID               string `gorm:"primaryKey;size:64"`
	UserId           int    `gorm:"index"`
	TokenId          int    `gorm:"index"`
	ConversationId   string `gorm:"size:64;index"`
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
	decoded, err := io.ReadAll(io.LimitReader(reader, responsesChatPayloadMaxDecoded+1))
	if err != nil {
		return "", fmt.Errorf("read compressed session: %w", err)
	}
	if len(decoded) > responsesChatPayloadMaxDecoded {
		return "", errors.New("decoded session exceeds size limit")
	}
	return string(decoded), nil
}

func GetResponsesChatSession(id string) (*ResponsesChatSession, error) {
	if id == "" {
		return nil, nil
	}
	var session ResponsesChatSession
	if err := DB.Where("id = ?", id).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if session.ConversationId != "" {
		var canonical ResponsesChatSession
		canonicalID := canonicalResponsesChatSessionID(session.ConversationId)
		if err := DB.Where("id = ?", canonicalID).First(&canonical).Error; err != nil {
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
	session.Items = items
	session.PendingToolCalls = pending
	return &session, nil
}

func SaveResponsesChatSession(session *ResponsesChatSession) error {
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

	// Preserve the legacy one-row format for callers without a stable thread ID.
	if session.ConversationId == "" {
		return DB.Save(session).Error
	}

	items, err := encodeResponsesChatPayload(session.Items)
	if err != nil {
		return err
	}
	pending, err := encodeResponsesChatPayload(session.PendingToolCalls)
	if err != nil {
		return err
	}

	mapping := *session
	mapping.Items = ""
	mapping.PendingToolCalls = ""
	mapping.CreatedAt = now
	mapping.UpdatedAt = now

	canonical := *session
	canonical.ID = canonicalResponsesChatSessionID(session.ConversationId)
	canonical.Items = items
	canonical.PendingToolCalls = pending
	canonical.CreatedAt = now
	canonical.UpdatedAt = now

	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&mapping).Error; err != nil {
			return err
		}
		return tx.Save(&canonical).Error
	})
}

func CleanupResponsesChatSessions(olderThanSeconds int64, batchSize int) (int64, error) {
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
	if err := DB.Model(&ResponsesChatSession{}).
		Where("created_at < ?", cutoff).
		Order("created_at asc").
		Limit(batchSize).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result := DB.Where("id IN ?", ids).Delete(&ResponsesChatSession{})
	return result.RowsAffected, result.Error
}
