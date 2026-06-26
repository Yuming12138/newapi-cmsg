package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type ResponsesChatSession struct {
	ID               string `gorm:"primaryKey;size:64"`
	UserId           int    `gorm:"index"`
	TokenId          int    `gorm:"index"`
	ModelName        string `gorm:"size:255"`
	Items            string `gorm:"type:text"`
	PendingToolCalls string `gorm:"type:text"`
	CreatedAt        int64  `gorm:"index"`
	UpdatedAt        int64  `gorm:"index"`
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
	return &session, nil
}

func SaveResponsesChatSession(session *ResponsesChatSession) error {
	if session == nil {
		return nil
	}
	now := common.GetTimestamp()
	if session.CreatedAt == 0 {
		session.CreatedAt = now
	}
	session.UpdatedAt = now
	return DB.Save(session).Error
}
