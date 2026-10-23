package models

import (
	"time"

	"github.com/google/uuid"
)

type ChatRow struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	LastReadAt      *time.Time
	DialogStartedAt *time.Time
	BotActive       bool
	CreatedAt       time.Time
}

type ChatListRow struct {
	ChatRow
	LastMessageText *string
	LastMessageAt   *time.Time
	Unread          int
}
type MessageRow struct {
	ID       uuid.UUID
	ChatID   uuid.UUID
	SenderID uuid.UUID
	Text     string
	SentAt   time.Time
	IsSystem bool
}

type MessageCursor struct {
	SentAt time.Time
	ID     uuid.UUID
	HasID  bool
}
