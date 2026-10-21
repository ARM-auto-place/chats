package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v4"
)

var (
	ErrChatNotFound = errors.New("chat not found")
	ErrBotSilenced  = errors.New("bot silenced")
)

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

// InsertMessage serializes writes per chat and atomically handles
// idempotency, participant creation, bot takeover, message persistence and
// the first-message dialog timestamp.
func (s *Storage) InsertMessage(
	ctx context.Context,
	chatID uuid.UUID,
	senderID uuid.UUID,
	clientMsgID uuid.UUID,
	text string,
	isAdmin bool,
	isBot bool,
) (MessageRow, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MessageRow{}, false, fmt.Errorf("begin insert message transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var botActive bool
	if err = tx.QueryRow(ctx, `SELECT bot_active FROM chats WHERE id = $1 FOR UPDATE`, chatID).Scan(&botActive); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MessageRow{}, false, ErrChatNotFound
		}
		return MessageRow{}, false, fmt.Errorf("lock chat for message: %w", err)
	}

	var existing MessageRow
	err = tx.QueryRow(ctx, `
		SELECT id, chat_id, sender_id, text, sent_at, is_system
		FROM messages
		WHERE chat_id = $1 AND sender_id = $2 AND client_msg_id = $3
	`, chatID, senderID, clientMsgID).Scan(
		&existing.ID, &existing.ChatID, &existing.SenderID, &existing.Text, &existing.SentAt, &existing.IsSystem,
	)
	if err == nil {
		if err = tx.Commit(ctx); err != nil {
			return MessageRow{}, false, fmt.Errorf("commit duplicate message lookup: %w", err)
		}
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return MessageRow{}, false, fmt.Errorf("lookup duplicate message: %w", err)
	}

	if isBot && !botActive {
		return MessageRow{}, false, ErrBotSilenced
	}

	if isAdmin || isBot {
		if _, err = tx.Exec(ctx, `
			INSERT INTO chat_participants (chat_id, user_id)
			VALUES ($1, $2)
			ON CONFLICT (chat_id, user_id) DO NOTHING
		`, chatID, senderID); err != nil {
			return MessageRow{}, false, fmt.Errorf("ensure message participant: %w", err)
		}
	}

	if isAdmin {
		if _, err = tx.Exec(ctx, `UPDATE chats SET bot_active = false WHERE id = $1`, chatID); err != nil {
			return MessageRow{}, false, fmt.Errorf("silence bot for admin message: %w", err)
		}
	}

	var message MessageRow
	if err = tx.QueryRow(ctx, `
		INSERT INTO messages (chat_id, sender_id, client_msg_id, text)
		VALUES ($1, $2, $3, $4)
		RETURNING id, chat_id, sender_id, text, sent_at, is_system
	`, chatID, senderID, clientMsgID, text).Scan(
		&message.ID, &message.ChatID, &message.SenderID, &message.Text, &message.SentAt, &message.IsSystem,
	); err != nil {
		return MessageRow{}, false, fmt.Errorf("insert message: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		UPDATE chats
		SET dialog_started_at = COALESCE(dialog_started_at, $2)
		WHERE id = $1
	`, chatID, message.SentAt); err != nil {
		return MessageRow{}, false, fmt.Errorf("start chat dialog: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return MessageRow{}, false, fmt.Errorf("commit insert message: %w", err)
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
func (s *Storage) InsertSystemMessage(ctx context.Context, chatID uuid.UUID, senderID uuid.UUID, text string) (MessageRow, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MessageRow{}, fmt.Errorf("begin insert system message transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var message MessageRow
	if err = tx.QueryRow(ctx, `
		INSERT INTO messages (chat_id, sender_id, client_msg_id, text, is_system)
		VALUES ($1, $2, $3, $4, true)
		RETURNING id, chat_id, sender_id, text, sent_at, is_system
	`, chatID, senderID, uuid.New(), text).Scan(
		&message.ID, &message.ChatID, &message.SenderID, &message.Text, &message.SentAt, &message.IsSystem,
	); err != nil {
		return MessageRow{}, fmt.Errorf("insert system message: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		UPDATE chats
		SET dialog_started_at = COALESCE(dialog_started_at, $2)
		WHERE id = $1
	`, chatID, message.SentAt); err != nil {
		return MessageRow{}, fmt.Errorf("start chat dialog for system message: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return MessageRow{}, fmt.Errorf("commit insert system message: %w", err)
	}
	return message, nil
}

// ListMessages returns up to limit messages older than before (or the newest
// limit messages if before is nil), newest-first. hasNext is true if there
// are older messages beyond what's returned.
func (s *Storage) ListMessages(ctx context.Context, chatID uuid.UUID, before *MessageCursor, limit int) ([]MessageRow, bool, error) {
	var beforeTime *time.Time
	var beforeID *uuid.UUID
	if before != nil {
		beforeTime = &before.SentAt
		if before.HasID {
			beforeID = &before.ID
		}
	}
	rows, err := s.pool.Query(ctx, `
			SELECT id, chat_id, sender_id, text, sent_at, is_system FROM messages
			WHERE chat_id = $1
			  AND (
			      $2::timestamptz IS NULL
			      OR sent_at < $2
			      OR ($3::uuid IS NOT NULL AND sent_at = $2 AND id < $3)
			  )
			ORDER BY sent_at DESC, id DESC
			LIMIT $4
		`, chatID, beforeTime, beforeID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	messages := make([]MessageRow, 0, limit+1)
	for rows.Next() {
		var m MessageRow
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
