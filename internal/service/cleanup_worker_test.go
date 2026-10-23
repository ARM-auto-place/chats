package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/ARM-auto-place/chats/pkg/models"
)

func TestCleanupWorker_Tick_CallsStorage(t *testing.T) {
	storage := &fakeStorage{cleanupIDs: []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}}
	w := NewCleanupWorker(storage, zerolog.Nop(), 0, nil)

	w.tick(context.Background())

	if storage.cleanupCalls != 1 {
		t.Fatalf("expected 1 call, got %d", storage.cleanupCalls)
	}
}

func TestCleanupWorker_Tick_LogsErrorWithoutPanicking(t *testing.T) {
	storage := &fakeStorage{cleanupErr: errors.New("db down")}
	w := NewCleanupWorker(storage, zerolog.Nop(), 0, nil)

	w.tick(context.Background()) // must not panic
}

func TestCleanupWorker_Tick_BroadcastsStageChangedPerResetChat(t *testing.T) {
	chatID := uuid.New()
	storage := &fakeStorage{cleanupIDs: []uuid.UUID{chatID}}
	hub := &fakeBroadcaster{}
	w := NewCleanupWorker(storage, zerolog.Nop(), 0, hub)

	w.tick(context.Background())

	if len(hub.frames) != 1 {
		t.Fatalf("expected a stage_changed frame to be broadcast, got %d", len(hub.frames))
	}
	var out models.WSOutgoing
	if err := json.Unmarshal(hub.frames[0], &out); err != nil {
		t.Fatalf("failed to decode broadcast: %v", err)
	}
	if out.Type != models.WSTypeStageChanged || out.ChatID != chatID {
		t.Fatalf("unexpected frame: %+v", out)
	}
}
