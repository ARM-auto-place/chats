package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v4"
)

type ChatRow struct {
	ID              uuid.UUID
	OwnerUserID     uuid.UUID
	OwnerLastReadAt *time.Time
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

const chatColumns = "id, owner_user_id, owner_last_read_at, dialog_started_at, bot_active, created_at"

func scanChatRow(row interface {
	Scan(dest ...interface{}) error
}) (ChatRow, error) {
	var c ChatRow
	err := row.Scan(&c.ID, &c.OwnerUserID, &c.OwnerLastReadAt, &c.DialogStartedAt, &c.BotActive, &c.CreatedAt)
	return c, err
}

// GetOrCreateChatByOwner returns userID's single support chat, creating it on
// first call. The no-op DO UPDATE is the standard idempotent get-or-create
// trick: ON CONFLICT DO NOTHING RETURNING yields no row on conflict, but
// DO UPDATE SET <col> = EXCLUDED.<col> always returns the (existing) row.
func (s *Storage) GetOrCreateChatByOwner(ctx context.Context, ownerUserID uuid.UUID) (ChatRow, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO chats (owner_user_id)
		VALUES ($1)
		ON CONFLICT (owner_user_id) DO UPDATE SET owner_user_id = EXCLUDED.owner_user_id
		RETURNING `+chatColumns, ownerUserID)

	c, err := scanChatRow(row)
	if err != nil {
		return ChatRow{}, fmt.Errorf("get or create chat by owner: %w", err)
	}
	return c, nil
}

func (s *Storage) GetChatByID(ctx context.Context, chatID uuid.UUID) (ChatRow, bool, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+chatColumns+` FROM chats WHERE id = $1`, chatID)

	c, err := scanChatRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ChatRow{}, false, nil
		}
		return ChatRow{}, false, fmt.Errorf("get chat by id: %w", err)
	}
	return c, true, nil
}

// ListQueue returns every chat (the support queue), newest activity first,
// with each row's unread count computed relative to viewerUserID's own
// read-receipt in chat_participants (a viewer who hasn't replied yet sees
// every owner message as unread).
func (s *Storage) ListQueue(ctx context.Context, viewerUserID uuid.UUID, limit int, offset int) ([]ChatListRow, int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			c.id, c.owner_user_id, c.owner_last_read_at, c.dialog_started_at, c.bot_active, c.created_at,
			lm.text, lm.sent_at,
			COALESCE(unread.cnt, 0) AS unread
		FROM chats c
		LEFT JOIN LATERAL (
			SELECT text, sent_at FROM messages WHERE chat_id = c.id ORDER BY sent_at DESC, id DESC LIMIT 1
		) lm ON true
		LEFT JOIN LATERAL (
			SELECT count(*) AS cnt FROM messages m
			WHERE m.chat_id = c.id AND m.sender_id = c.owner_user_id
			  AND m.sent_at > COALESCE(
			      (SELECT last_read_at FROM chat_participants WHERE chat_id = c.id AND user_id = $1),
			      '-infinity'::timestamptz
			  )
		) unread ON true
		ORDER BY COALESCE(lm.sent_at, c.created_at) DESC, c.id DESC
		LIMIT $2 OFFSET $3
	`, viewerUserID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list queue: %w", err)
	}
	defer rows.Close()

	items := make([]ChatListRow, 0, limit)
	for rows.Next() {
		var item ChatListRow
		if err = rows.Scan(
			&item.ID, &item.OwnerUserID, &item.OwnerLastReadAt, &item.DialogStartedAt, &item.BotActive, &item.CreatedAt,
			&item.LastMessageText, &item.LastMessageAt, &item.Unread,
		); err != nil {
			return nil, 0, fmt.Errorf("scan queue row: %w", err)
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate queue: %w", err)
	}

	var total int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM chats`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count chats: %w", err)
	}

	return items, total, nil
}

func (s *Storage) SetBotActive(ctx context.Context, chatID uuid.UUID, active bool) error {
	if _, err := s.pool.Exec(ctx, `UPDATE chats SET bot_active = $2 WHERE id = $1`, chatID, active); err != nil {
		return fmt.Errorf("set bot active: %w", err)
	}
	return nil
}

func (s *Storage) MarkOwnerRead(ctx context.Context, chatID uuid.UUID) error {
	if _, err := s.pool.Exec(ctx, `UPDATE chats SET owner_last_read_at = now() WHERE id = $1`, chatID); err != nil {
		return fmt.Errorf("mark owner read: %w", err)
	}
	return nil
}

func (s *Storage) UpsertParticipantRead(ctx context.Context, chatID uuid.UUID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat_participants (chat_id, user_id, last_read_at)
		VALUES ($1, $2, now())
		ON CONFLICT (chat_id, user_id) DO UPDATE SET last_read_at = now()
	`, chatID, userID)
	if err != nil {
		return fmt.Errorf("upsert participant read: %w", err)
	}
	return nil
}
