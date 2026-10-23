package models

import (
	"time"

	chatModels "github.com/ARM-auto-place/chats/pkg/models"
	"github.com/google/uuid"
)

// SuccessData is the union of the four REST data payloads. tg currently
// supports one response schema per service interface, so optional fields
// describe the endpoint-specific alternatives without documenting data as an
// untyped object.
type SuccessData struct {
	ID              uuid.UUID                 `json:"id,omitempty"`
	OwnerUserID     uuid.UUID                 `json:"owner_user_id,omitempty"`
	DialogStartedAt *time.Time                `json:"dialog_started_at,omitempty"`
	BotActive       *bool                     `json:"bot_active,omitempty"`
	CreatedAt       *time.Time                `json:"created_at,omitempty"`
	Chats           []chatModels.ChatListItem `json:"chats,omitempty"`
	Messages        []chatModels.Message      `json:"messages,omitempty"`
	HasNext         *bool                     `json:"has_next,omitempty"`
}

type Resp200 struct {
	// @tg desc=`Флаг показывающий, что ответ пришел с ошибкой`
	// @tg example=false
	Error bool `json:"error"`
	// @tg example=``
	ErrorText string      `json:"errorText"`
	Data      SuccessData `json:"data"`
	// @tg example=``
	AdditionalErrors interface{} `json:"additionalErrors"`
}
