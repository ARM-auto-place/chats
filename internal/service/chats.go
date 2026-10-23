package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	customErrors "github.com/ARM-auto-place/chats/internal/errors"
	internalModels "github.com/ARM-auto-place/chats/internal/models"
	"github.com/ARM-auto-place/chats/pkg/models"
)

const (
	StageOperator = "operator"
	StageBot      = "bot"
)

func chatRowToModel(r internalModels.ChatRow) models.Chat {
	return models.Chat{
		ID:              r.ID,
		UserID:          r.UserID,
		DialogStartedAt: r.DialogStartedAt,
		BotActive:       r.BotActive,
		CreatedAt:       r.CreatedAt,
	}
}

func messageRowToModel(r internalModels.MessageRow) models.Message {
	return models.Message{ID: r.ID, ChatID: r.ChatID, SenderID: r.SenderID, Text: r.Text, SentAt: r.SentAt, IsSystem: r.IsSystem}
}

func (s *service) GetOrCreateSupportChat(ctx context.Context, userID uuid.UUID) (models.Chat, error) {
	return s.EnsureOwnerChat(ctx, userID)
}

func (s *service) ListSupportQueue(ctx context.Context, userID uuid.UUID, limit int, offset int) (models.ChatListResponse, error) {
	if userID == uuid.Nil {
		return models.ChatListResponse{}, customErrors.UnauthorizedError()
	}
	if offset < 0 {
		return models.ChatListResponse{}, customErrors.BadRequestError().AddCause("field", "offset")
	}
	if err := s.checkAdminAccess(ctx, userID); err != nil {
		return models.ChatListResponse{}, err
	}
	if limit <= 0 || limit > maxQueueLimit {
		limit = defaultQueueLimit
	}

	rows, total, err := s.storage.ListQueue(ctx, userID, limit, offset)
	if err != nil {
		s.logger.Error().Err(err).Str("userID", userID.String()).Str("event", "support_queue_list").Msg("chat persistence failed")
		return models.ChatListResponse{}, customErrors.InternalServerError().SetOuterError(err)
	}

	items := make([]models.ChatListItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, models.ChatListItem{
			Chat:            chatRowToModel(r.ChatRow),
			LastMessageText: r.LastMessageText,
			LastMessageAt:   r.LastMessageAt,
			Unread:          r.Unread,
		})
	}
	return models.ChatListResponse{Chats: items, HasNext: offset+limit < total}, nil
}

func (s *service) GetMessages(ctx context.Context, userID uuid.UUID, chatID uuid.UUID, before string, limit int) (models.MessagesResponse, error) {
	if userID == uuid.Nil {
		return models.MessagesResponse{}, customErrors.UnauthorizedError()
	}
	if chatID == uuid.Nil {
		return models.MessagesResponse{}, customErrors.BadRequestError().AddCause("field", "chatID")
	}
	chat, found, err := s.storage.GetChatByID(ctx, chatID)
	if err != nil {
		s.logger.Error().Err(err).Str("chatID", chatID.String()).Str("userID", userID.String()).Str("event", "chat_lookup_for_history").Msg("chat persistence failed")
		return models.MessagesResponse{}, customErrors.InternalServerError().SetOuterError(err)
	}
	if !found {
		return models.MessagesResponse{}, customErrors.BadRequestError().AddCause("field", "chatID")
	}
	if userID != chat.UserID {
		if err = s.checkAdminAccess(ctx, userID); err != nil {
			return models.MessagesResponse{}, err
		}
	}

	var cursor *internalModels.MessageCursor
	if before != "" {
		parts := strings.Split(before, ",")
		if len(parts) > 2 {
			return models.MessagesResponse{}, customErrors.BadRequestError().AddCause("field", "before")
		}
		t, parseErr := time.Parse(time.RFC3339Nano, parts[0])
		if parseErr != nil {
			return models.MessagesResponse{}, customErrors.BadRequestError().AddCause("field", "before")
		}
		cursor = &internalModels.MessageCursor{SentAt: t}
		if len(parts) == 2 {
			id, idErr := uuid.Parse(parts[1])
			if idErr != nil || id == uuid.Nil {
				return models.MessagesResponse{}, customErrors.BadRequestError().AddCause("field", "before")
			}
			cursor.ID = id
			cursor.HasID = true
		}
	}
	if limit <= 0 || limit > maxMessagesLimit {
		limit = defaultMessagesLimit
	}

	rows, hasNext, err := s.storage.ListMessages(ctx, chatID, cursor, limit)
	if err != nil {
		s.logger.Error().Err(err).Str("chatID", chatID.String()).Str("userID", userID.String()).Str("event", "message_history_list").Msg("chat persistence failed")
		return models.MessagesResponse{}, customErrors.InternalServerError().SetOuterError(err)
	}

	messages := make([]models.Message, len(rows))
	for i, r := range rows {
		messages[len(rows)-1-i] = messageRowToModel(r)
	}
	return models.MessagesResponse{Messages: messages, HasNext: hasNext}, nil
}

func (s *service) MarkRead(ctx context.Context, userID uuid.UUID, chatID uuid.UUID) (bool, error) {
	if _, err := s.MarkChatRead(ctx, userID, chatID); err != nil {
		return false, err
	}
	return true, nil
}

// SetChatStage lets the chat's owner or an Admin explicitly move a dialog
// between "operator" (bot silenced right away, no need to wait for an Admin
// to type) and "bot" (bot re-enabled, without touching message history —
// that's the cleanup worker's job, not this endpoint's).
func (s *service) SetChatStage(ctx context.Context, userID uuid.UUID, chatID uuid.UUID, stage string) (models.Chat, error) {
	if userID == uuid.Nil {
		return models.Chat{}, customErrors.UnauthorizedError()
	}
	if chatID == uuid.Nil {
		return models.Chat{}, customErrors.BadRequestError().AddCause("field", "chatID")
	}
	var targetBotActive bool
	switch stage {
	case StageOperator:
		targetBotActive = false
	case StageBot:
		targetBotActive = true
	default:
		return models.Chat{}, customErrors.BadRequestError().AddCause("field", "stage")
	}

	chat, found, err := s.storage.GetChatByID(ctx, chatID)
	if err != nil {
		s.logger.Error().Err(err).Str("chatID", chatID.String()).Str("userID", userID.String()).Str("event", "chat_lookup_for_stage").Msg("chat persistence failed")
		return models.Chat{}, customErrors.InternalServerError().SetOuterError(err)
	}
	if !found {
		return models.Chat{}, customErrors.BadRequestError().AddCause("field", "chatID")
	}
	if userID != chat.UserID {
		if err = s.checkAdminAccess(ctx, userID); err != nil {
			return models.Chat{}, err
		}
	}

	if chat.BotActive == targetBotActive {
		return chatRowToModel(chat), nil
	}

	if err = s.storage.SetBotActive(ctx, chatID, targetBotActive); err != nil {
		s.logger.Error().Err(err).Str("chatID", chatID.String()).Str("userID", userID.String()).Str("event", "chat_stage_set").Msg("chat persistence failed")
		return models.Chat{}, customErrors.InternalServerError().SetOuterError(err)
	}
	chat.BotActive = targetBotActive

	systemText := "Диалог возвращён боту"
	if stage == StageOperator {
		systemText = "Клиент запросил оператора"
	}
	sysMsg, err := s.storage.InsertSystemMessage(ctx, chatID, BotUserID, systemText)
	if err != nil {
		s.logger.Error().Err(err).Str("chatID", chatID.String()).Str("userID", userID.String()).Str("event", "chat_stage_system_message").Msg("chat persistence failed")
		return models.Chat{}, customErrors.InternalServerError().SetOuterError(err)
	}

	s.broadcastStageChanged(ctx, chatID, targetBotActive, stage, userID, sysMsg.SentAt)
	return chatRowToModel(chat), nil
}

// broadcastStageChanged fans a stage_changed WS frame out to every
// subscriber of chatID. Called both from SetChatStage (explicit API) and
// from SendMessage's admin-auto-silence path — see messages.go.
func (s *service) broadcastStageChanged(ctx context.Context, chatID uuid.UUID, botActive bool, stage string, changedBy uuid.UUID, changedAt time.Time) {
	if s.hub == nil {
		return
	}
	data, err := json.Marshal(models.WSOutgoing{
		Type:   models.WSTypeStageChanged,
		ChatID: chatID,
		Meta: models.StageChangedMeta{
			BotActive: botActive,
			Stage:     stage,
			ChangedBy: changedBy,
			ChangedAt: changedAt,
		},
	})
	if err != nil {
		s.logger.Error().Err(err).Str("chatID", chatID.String()).Str("event", "ws_stage_changed_marshal").Msg("failed to marshal stage_changed frame")
		return
	}
	if err = s.hub.Broadcast(ctx, chatID, data, "stage_changed"); err != nil {
		s.logger.Warn().Err(err).Str("chatID", chatID.String()).Str("event", "ws_stage_changed_broadcast").Msg("failed to broadcast stage_changed frame")
	}
}
