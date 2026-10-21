package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// CleanupExpiredDialogs wipes message history for every chat whose dialog
// started 24h+ ago, per the design spec's "Автоочистка и статус бота": the
// chat and its participants are left in place, only messages and the dialog
// timer are cleared, and bot_active resets to true so the bot can speak again
// on the next dialog. Returns the IDs of the chats reset (nil if none were
// due), so the caller can notify their WS subscribers.
func (s *Storage) CleanupExpiredDialogs(ctx context.Context) ([]uuid.UUID, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin cleanup expired dialogs: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id FROM chats
		WHERE dialog_started_at IS NOT NULL
		  AND dialog_started_at < now() - INTERVAL '24 hours'
		FOR UPDATE
	`)
	if err != nil {
		return nil, fmt.Errorf("lock expired dialogs: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan expired dialog: %w", err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate expired dialogs: %w", err)
	}
	rows.Close()

	if len(ids) > 0 {
		if _, err = tx.Exec(ctx, `DELETE FROM messages WHERE chat_id = ANY($1::uuid[])`, ids); err != nil {
			return nil, fmt.Errorf("delete expired dialog messages: %w", err)
		}
		if _, err = tx.Exec(ctx, `UPDATE chats SET dialog_started_at = NULL, bot_active = true WHERE id = ANY($1::uuid[])`, ids); err != nil {
			return nil, fmt.Errorf("reset expired dialogs: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit cleanup expired dialogs: %w", err)
	}
	return ids, nil
}
