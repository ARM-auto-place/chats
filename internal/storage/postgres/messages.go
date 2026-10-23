package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ARM-auto-place/chats/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v4"
)

var (
	ErrChatNotFound = errors.New("chat not found")
	ErrBotSilenced  = errors.New("bot silenced")
)

// InsertMessage serializes writes per chat and atomically handles
// idempotency, participant creation, bot takeover, message persistence and
// the first-message dialog timestamp.
func (s *storage) InsertMessage(
	ctx context.Context,
	chatID uuid.UUID,
	senderID uuid.UUID,
	clientMsgID uuid.UUID,
	text string,
	isAdmin bool,
	isBot bool,
) (models.MessageRow, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.MessageRow{}, false, fmt.Errorf("begin insert message transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var botActive bool
	if err = tx.QueryRow(ctx, SqlSelectBotStatus, chatID).Scan(&botActive); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.MessageRow{}, false, ErrChatNotFound
		}
		return models.MessageRow{}, false, fmt.Errorf("lock chat for message: %w", err)
	}

	var existing models.MessageRow
	err = tx.QueryRow(ctx, SqlSelectMessageInChat, chatID, senderID, clientMsgID).Scan(
		&existing.ID, &existing.ChatID, &existing.SenderID, &existing.Text, &existing.SentAt, &existing.IsSystem,
	)
	if err == nil {
		if err = tx.Commit(ctx); err != nil {
			return models.MessageRow{}, false, fmt.Errorf("commit duplicate message lookup: %w", err)
		}
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return models.MessageRow{}, false, fmt.Errorf("lookup duplicate message: %w", err)
	}

	if isBot && !botActive {
		return models.MessageRow{}, false, ErrBotSilenced
	}

	if isAdmin || isBot {
		if _, err = tx.Exec(ctx, SqlInsertChatParticipants, chatID, senderID); err != nil {
			return models.MessageRow{}, false, fmt.Errorf("ensure message participant: %w", err)
		}
	}

	if isAdmin {
		if _, err = tx.Exec(ctx, sqlUpsetBotStatus, chatID, false); err != nil {
			return models.MessageRow{}, false, fmt.Errorf("silence bot for admin message: %w", err)
		}
	}

	var message models.MessageRow
	if err = tx.QueryRow(ctx, SqlInsertMessages, chatID, senderID, clientMsgID, text).Scan(
		&message.ID, &message.ChatID, &message.SenderID, &message.Text, &message.SentAt, &message.IsSystem,
	); err != nil {
		return models.MessageRow{}, false, fmt.Errorf("insert message: %w", err)
	}

	if _, err = tx.Exec(ctx, SqlUpdateDialogStarted, chatID, message.SentAt); err != nil {
		return models.MessageRow{}, false, fmt.Errorf("start chat dialog: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return models.MessageRow{}, false, fmt.Errorf("commit insert message: %w", err)
	}
	return message, true, nil
}

// InsertSystemMessage writes a server-generated stage-change notice
// (senderID is service.BotUserID). Unlike InsertMessage it never checks
// bot_active (a system notice fires exactly when that flag changes) and
// isn't idempotency-keyed on a client-supplied ID — each call gets a fresh
// client_msg_id, since callers never retry it. It still stamps
// dialog_started_at so the chat surfaces in the support queue like a real
// message would.
func (s *storage) InsertSystemMessage(ctx context.Context, chatID uuid.UUID, senderID uuid.UUID, text string) (models.MessageRow, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.MessageRow{}, fmt.Errorf("begin insert system message transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var message models.MessageRow
	if err = tx.QueryRow(ctx, SqlInsertMessagesSystem, chatID, senderID, uuid.New(), text).Scan(
		&message.ID, &message.ChatID, &message.SenderID, &message.Text, &message.SentAt, &message.IsSystem,
	); err != nil {
		return models.MessageRow{}, fmt.Errorf("insert system message: %w", err)
	}

	if _, err = tx.Exec(ctx, SqlUpdateDialogStarted, chatID, message.SentAt); err != nil {
		return models.MessageRow{}, fmt.Errorf("start chat dialog for system message: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return models.MessageRow{}, fmt.Errorf("commit insert system message: %w", err)
	}
	return message, nil
}

// ListMessages returns up to limit messages older than before (or the newest
// limit messages if before is nil), newest-first. hasNext is true if there
// are older messages beyond what's returned.
func (s *storage) ListMessages(ctx context.Context, chatID uuid.UUID, before *models.MessageCursor, limit int) ([]models.MessageRow, bool, error) {
	var beforeTime *time.Time
	var beforeID *uuid.UUID
	if before != nil {
		beforeTime = &before.SentAt
		if before.HasID {
			beforeID = &before.ID
		}
	}
	rows, err := s.pool.Query(ctx, SqlSelectChatList, chatID, beforeTime, beforeID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	messages := make([]models.MessageRow, 0, limit+1)
	for rows.Next() {
		var m models.MessageRow
		if err = rows.Scan(&m.ID, &m.ChatID, &m.SenderID, &m.Text, &m.SentAt, &m.IsSystem); err != nil {
			return nil, false, fmt.Errorf("scan message: %w", err)
		}
		messages = append(messages, m)
	}
	if err = rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate messages: %w", err)
	}

	hasNext := len(messages) > limit
	if hasNext {
		messages = messages[:limit]
	}
	return messages, hasNext, nil
}
