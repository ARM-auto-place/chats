package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/ARM-auto-place/chats/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v4"
)

func scanChatRow(row interface {
	Scan(dest ...interface{}) error
}) (models.ChatRow, error) {
	var c models.ChatRow
	err := row.Scan(&c.ID, &c.UserID, &c.LastReadAt, &c.DialogStartedAt, &c.BotActive, &c.CreatedAt)
	return c, err
}

// GetOrCreateChatByOwner returns userID's single support chat, creating it on
// first call. The no-op DO UPDATE is the standard idempotent get-or-create
// trick: ON CONFLICT DO NOTHING RETURNING yields no row on conflict
func (s *storage) GetOrCreateChatByOwner(ctx context.Context, ownerUserID uuid.UUID) (models.ChatRow, error) {
	row := s.pool.QueryRow(ctx, sqlInsertChatByUser, ownerUserID)

	c, err := scanChatRow(row)
	if err != nil {
		return models.ChatRow{}, fmt.Errorf("get or create chat by owner: %w", err)
	}
	return c, nil
}

func (s *storage) GetChatByID(ctx context.Context, chatID uuid.UUID) (models.ChatRow, bool, error) {
	row := s.pool.QueryRow(ctx, sqlSelectChatByID, chatID)

	c, err := scanChatRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ChatRow{}, false, nil
		}
		return models.ChatRow{}, false, fmt.Errorf("get chat by id: %w", err)
	}
	return c, true, nil
}

// ListQueue returns every chat (the support queue), newest activity first,
// with each row's unread count computed relative to viewerUserID's own
// read-receipt in chat_participants (a viewer who hasn't replied yet sees
// every owner message as unread).
func (s *storage) ListQueue(ctx context.Context, viewerUserID uuid.UUID, limit int, offset int) ([]models.ChatListRow, int, error) {
	rows, err := s.pool.Query(ctx, sqlSelectChatsByUserID, viewerUserID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list queue: %w", err)
	}
	defer rows.Close()

	items := make([]models.ChatListRow, 0, limit)
	for rows.Next() {
		var item models.ChatListRow
		if err = rows.Scan(
			&item.ID, &item.UserID, &item.LastReadAt, &item.DialogStartedAt, &item.BotActive, &item.CreatedAt,
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
	if err = s.pool.QueryRow(ctx, sqlSelectChatsCount).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count chats: %w", err)
	}

	return items, total, nil
}

func (s *storage) SetBotActive(ctx context.Context, chatID uuid.UUID, active bool) error {
	if _, err := s.pool.Exec(ctx, sqlUpsetBotStatus, chatID, active); err != nil {
		return fmt.Errorf("set bot active: %w", err)
	}
	return nil
}

func (s *storage) MarkUserRead(ctx context.Context, chatID uuid.UUID) error {
	if _, err := s.pool.Exec(ctx, sqlUpsetUserRead, chatID); err != nil {
		return fmt.Errorf("mark user read: %w", err)
	}
	return nil
}

func (s *storage) UpsertParticipantRead(ctx context.Context, chatID uuid.UUID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, SqlUpsettParticipantRead, chatID, userID)
	if err != nil {
		return fmt.Errorf("upsert participant read: %w", err)
	}
	return nil
}
