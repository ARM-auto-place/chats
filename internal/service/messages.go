package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	customErrors "github.com/ARM-auto-place/chats/internal/errors"
	"github.com/ARM-auto-place/chats/internal/storage/postgres"
	"github.com/ARM-auto-place/chats/pkg/models"
)

// BotUserID is the fixed system user seeded by the chat_service migration
// (back/migrations/pkg/migrations/data/20260914120000_chat_service.sql).
var BotUserID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// ChatBackend is the subset of *service the WS layer (internal/service/ws) needs.
// A single concrete *service value returned by New satisfies both this and
// externalapi.ChatAPI — see the comment on New in service.go.
type ChatBackend interface {
	EnsureOwnerChat(ctx context.Context, userID uuid.UUID) (models.Chat, error)
	SendMessage(ctx context.Context, senderUserID uuid.UUID, chatID uuid.UUID, clientMsgID uuid.UUID, text string) (models.Message, bool, error)
	MarkChatRead(ctx context.Context, userID uuid.UUID, chatID uuid.UUID) (time.Time, error)
}

func (s *service) EnsureOwnerChat(ctx context.Context, userID uuid.UUID) (models.Chat, error) {
	if userID == uuid.Nil {
		return models.Chat{}, customErrors.UnauthorizedError()
	}
	row, err := s.storage.GetOrCreateChatByOwner(ctx, userID)
	if err != nil {
		s.logger.Error().Err(err).Str("userID", userID.String()).Str("event", "chat_get_or_create").Msg("chat persistence failed")
		return models.Chat{}, customErrors.InternalServerError().SetOuterError(err)
	}
	return chatRowToModel(row), nil
}

// SendMessage enforces, server-side, who may write into a chat and whether
// the bot is currently allowed to speak — see docs/superpowers/specs/2026-09-14-chat-service-design.md's
// "Автоочистка и статус бота" section. An Admin sending a message silences
// the bot in that chat until the next 24h auto-cleanup resets it.
func (s *service) SendMessage(ctx context.Context, senderUserID uuid.UUID, chatID uuid.UUID, clientMsgID uuid.UUID, text string) (models.Message, bool, error) {
	if senderUserID == uuid.Nil || chatID == uuid.Nil || clientMsgID == uuid.Nil {
		return models.Message{}, false, customErrors.BadRequestError().AddCause("field", "uuid")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return models.Message{}, false, customErrors.BadRequestError().AddCause("field", "text")
	}

	chat, found, err := s.storage.GetChatByID(ctx, chatID)
	if err != nil {
		s.logger.Error().Err(err).Str("chatID", chatID.String()).Str("userID", senderUserID.String()).Str("event", "chat_lookup_for_message").Msg("chat persistence failed")
		return models.Message{}, false, customErrors.InternalServerError().SetOuterError(err)
	}
	if !found {
		return models.Message{}, false, customErrors.NotFoundError()
	}

	isOwner := senderUserID == chat.UserID
	isBot := senderUserID == BotUserID
	isAdmin := false
	if !isOwner && !isBot {
		isAdmin, err = s.isAdmin(ctx, senderUserID)
		if err != nil {
			return models.Message{}, false, err
		}
	}
	if !isOwner && !isAdmin && !isBot {
		return models.Message{}, false, customErrors.ForbiddenError()
	}

	row, created, err := s.storage.InsertMessage(ctx, chatID, senderUserID, clientMsgID, text, isAdmin, isBot)
	if err != nil {
		if errors.Is(err, postgres.ErrBotSilenced) {
			s.logger.Info().Str("chatID", chatID.String()).Str("userID", senderUserID.String()).Str("event", "bot_message_suppressed").Msg("chat bot message suppressed")
			return models.Message{}, false, customErrors.ForbiddenError().AddCause("reason", "bot_silenced")
		}
		if errors.Is(err, postgres.ErrChatNotFound) {
			return models.Message{}, false, customErrors.NotFoundError()
		}
		s.logger.Error().Err(err).Str("chatID", chatID.String()).Str("userID", senderUserID.String()).Str("event", "message_insert").Msg("chat message persistence failed")
		return models.Message{}, false, customErrors.InternalServerError().SetOuterError(err)
	}

	// chat.BotActive is this chat's state as read before the insert above; an
	// Admin's message unconditionally sets bot_active = false (see
	// InsertMessage), so it just flipped iff it was true here.
	if isAdmin && chat.BotActive {
		s.broadcastStageChanged(ctx, chatID, false, StageOperator, senderUserID, row.SentAt)
	}
	if isOwner && created && chat.BotActive && s.assistant != nil {
		s.replyAsBot(ctx, chatID, text)
	}

	return messageRowToModel(row), created, nil
}

// replyAsBot asks the assistant in the background and posts its answer as
// BotUserID. It goes through SendMessage, so an Admin who took the chat over
// while the assistant was thinking still silences the reply (ErrBotSilenced).
// ctx is detached from cancellation: the caller is usually a WS worker whose
// context dies with the connection, but the reply must still land in history.
func (s *service) replyAsBot(ctx context.Context, chatID uuid.UUID, text string) {
	ctx = context.WithoutCancel(ctx)
	s.botReplies.Go(func() {
		ctx, cancel := context.WithTimeout(ctx, s.assistantTimeout)
		defer cancel()

		answer, err := s.assistant.Chat(ctx, text, chatID.String())
		if err != nil {
			s.logger.Warn().Err(err).Str("chatID", chatID.String()).Str("event", "assistant_chat").Msg("assistant request failed")
			return
		}

		msg, created, err := s.SendMessage(ctx, BotUserID, chatID, uuid.New(), answer.Text)
		if err != nil {
			s.logger.Warn().Err(err).Str("chatID", chatID.String()).Str("event", "assistant_reply").Msg("assistant reply not posted")
			return
		}
		if !created || s.hub == nil {
			return
		}
		data, err := json.Marshal(models.WSOutgoing{Type: models.WSTypeMessage, ChatID: chatID, Message: &msg})
		if err != nil {
			s.logger.Error().Err(err).Str("chatID", chatID.String()).Str("event", "ws_assistant_reply_marshal").Msg("failed to marshal assistant reply frame")
			return
		}
		if err = s.hub.Broadcast(ctx, chatID, data, "message"); err != nil {
			s.logger.Warn().Err(err).Str("chatID", chatID.String()).Str("event", "ws_assistant_reply_broadcast").Msg("failed to broadcast assistant reply")
		}
	})
}

// MarkChatRead updates the caller's read-receipt: chats.owner_last_read_at
// for the chat's owner, or a chat_participants row for an Admin/the bot.
func (s *service) MarkChatRead(ctx context.Context, userID uuid.UUID, chatID uuid.UUID) (time.Time, error) {
	if userID == uuid.Nil {
		return time.Time{}, customErrors.UnauthorizedError()
	}
	if chatID == uuid.Nil {
		return time.Time{}, customErrors.BadRequestError().AddCause("field", "chatID")
	}
	chat, found, err := s.storage.GetChatByID(ctx, chatID)
	if err != nil {
		s.logger.Error().Err(err).Str("chatID", chatID.String()).Str("userID", userID.String()).Str("event", "chat_lookup_for_read").Msg("chat persistence failed")
		return time.Time{}, customErrors.InternalServerError().SetOuterError(err)
	}
	if !found {
		return time.Time{}, customErrors.BadRequestError().AddCause("field", "chatID")
	}

	if userID == chat.UserID {
		if err = s.storage.MarkUserRead(ctx, chatID); err != nil {
			s.logger.Error().Err(err).Str("chatID", chatID.String()).Str("userID", userID.String()).Str("event", "owner_mark_read").Msg("chat persistence failed")
			return time.Time{}, customErrors.InternalServerError().SetOuterError(err)
		}
		return time.Now(), nil
	}

	isBot := userID == BotUserID
	isAdmin := false
	if !isBot {
		isAdmin, err = s.isAdmin(ctx, userID)
		if err != nil {
			return time.Time{}, err
		}
	}
	if !isAdmin && !isBot {
		return time.Time{}, customErrors.ForbiddenError()
	}
	if err = s.storage.UpsertParticipantRead(ctx, chatID, userID); err != nil {
		s.logger.Error().Err(err).Str("chatID", chatID.String()).Str("userID", userID.String()).Str("event", "participant_mark_read").Msg("chat persistence failed")
		return time.Time{}, customErrors.InternalServerError().SetOuterError(err)
	}
	return time.Now(), nil
}
