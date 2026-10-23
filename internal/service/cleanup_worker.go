package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog"

	"github.com/ARM-auto-place/chats/internal/storage/postgres"
	"github.com/ARM-auto-place/chats/pkg/models"
)

// CleanupWorker periodically wipes message history for chats whose dialog
// started more than 24h ago (see the design spec's "Автоочистка и статус
// бота") — the chat and its participants are left in place. It also notifies
// each affected chat's WS subscribers, since bot_active silently flips back
// to true here — without this, the frontend would only find out on its next
// poll.
type CleanupWorker struct {
	storage  postgres.Storage
	logger   zerolog.Logger
	interval time.Duration
	hub      Broadcaster // nil is fine: broadcasts are then just skipped.
}

func NewCleanupWorker(storage postgres.Storage, logger zerolog.Logger, interval time.Duration, hub Broadcaster) *CleanupWorker {
	return &CleanupWorker{storage: storage, logger: logger, interval: interval, hub: hub}
}

// Run blocks, ticking every w.interval, until ctx is cancelled.
func (w *CleanupWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *CleanupWorker) tick(ctx context.Context) {
	ids, err := w.storage.CleanupExpiredDialogs(ctx)
	if err != nil {
		w.logger.Error().Err(err).Str("event", "expired_dialog_cleanup").Msg("cleanup worker: failed to expire dialogs")
		return
	}
	if len(ids) == 0 {
		return
	}
	w.logger.Info().Str("event", "expired_dialog_cleanup").Int("chats_cleared", len(ids)).Msg("cleanup worker: cleared expired dialogs")
	if w.hub == nil {
		return
	}
	now := time.Now()
	for _, chatID := range ids {
		data, marshalErr := json.Marshal(models.WSOutgoing{
			Type:   models.WSTypeStageChanged,
			ChatID: chatID,
			Meta: models.StageChangedMeta{
				BotActive: true,
				Stage:     StageBot,
				ChangedBy: BotUserID,
				ChangedAt: now,
			},
		})
		if marshalErr != nil {
			w.logger.Error().Err(marshalErr).Str("chatID", chatID.String()).Str("event", "ws_stage_changed_marshal").Msg("failed to marshal stage_changed frame")
			continue
		}
		if err = w.hub.Broadcast(ctx, chatID, data, "stage_changed"); err != nil {
			w.logger.Warn().Err(err).Str("chatID", chatID.String()).Str("event", "ws_stage_changed_broadcast").Msg("failed to broadcast stage_changed frame")
		}
	}
}
