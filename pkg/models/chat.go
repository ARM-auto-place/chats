package models

import (
	"time"

	"github.com/google/uuid"
)

type Chat struct {
	ID              uuid.UUID  `json:"id"`
	UserID          uuid.UUID  `json:"userId"`
	DialogStartedAt *time.Time `json:"dialogStartedAt"`
	BotActive       bool       `json:"botActive"`
	CreatedAt       time.Time  `json:"createdAt"`
}

type Message struct {
	ID       uuid.UUID `json:"id"`
	ChatID   uuid.UUID `json:"chatId"`
	SenderID uuid.UUID `json:"senderId"`
	Text     string    `json:"text"`
	SentAt   time.Time `json:"sentAt"`
	IsSystem bool      `json:"isSystem"`
}

type ChatListItem struct {
	Chat
	LastMessageText *string    `json:"lastMessageText"`
	LastMessageAt   *time.Time `json:"lastMessageAt"`
	Unread          int        `json:"unread"`
}

type ChatListResponse struct {
	Chats   []ChatListItem `json:"chats"`
	HasNext bool           `json:"hasNext"`
}

type MessagesResponse struct {
	Messages []Message `json:"messages"`
	HasNext  bool      `json:"hasNext"`
}
