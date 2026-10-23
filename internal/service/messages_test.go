package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	customErrors "github.com/ARM-auto-place/chats/internal/errors"
	"github.com/ARM-auto-place/chats/internal/models"
	"github.com/ARM-auto-place/chats/internal/storage/postgres"
	pkgModels "github.com/ARM-auto-place/chats/pkg/models"
)

func TestSendMessage_OwnerCanAlwaysSend(t *testing.T) {
	chatID := uuid.New()
	ownerID := uuid.New()
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: ownerID, BotActive: true}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: false}, nil)

	_, _, err := svc.SendMessage(context.Background(), ownerID, chatID, uuid.New(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendMessage_AdminCanSendAndSilencesBot(t *testing.T) {
	chatID := uuid.New()
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: uuid.New(), BotActive: true}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: true}, nil)

	_, _, err := svc.SendMessage(context.Background(), uuid.New(), chatID, uuid.New(), "hi from support")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !storage.insertIsAdmin || storage.insertIsBot {
		t.Fatalf("expected admin flags, got admin=%v bot=%v", storage.insertIsAdmin, storage.insertIsBot)
	}
}

func TestSendMessage_AdminAutoSilence_BroadcastsStageChanged(t *testing.T) {
	chatID := uuid.New()
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: uuid.New(), BotActive: true}, chatFound: true}
	hub := &fakeBroadcaster{}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: true}, hub)

	if _, _, err := svc.SendMessage(context.Background(), uuid.New(), chatID, uuid.New(), "hi from support"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(hub.frames) != 1 || hub.chatIDs[0] != chatID {
		t.Fatalf("expected one stage_changed broadcast on admin takeover, got %d", len(hub.frames))
	}
	var out pkgModels.WSOutgoing
	if err := json.Unmarshal(hub.frames[0], &out); err != nil {
		t.Fatalf("failed to decode broadcast: %v", err)
	}
	if out.Type != pkgModels.WSTypeStageChanged {
		t.Fatalf("expected stage_changed frame, got %+v", out)
	}
}

func TestSendMessage_AdminSecondMessage_NoDuplicateBroadcast(t *testing.T) {
	chatID := uuid.New()
	// bot_active already false: the chat's already-taken-over state, so a
	// second Admin message must not re-broadcast a stage change.
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: uuid.New(), BotActive: false}, chatFound: true}
	hub := &fakeBroadcaster{}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: true}, hub)

	if _, _, err := svc.SendMessage(context.Background(), uuid.New(), chatID, uuid.New(), "still here"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(hub.frames) != 0 {
		t.Fatalf("expected no broadcast, got %s", hub.frames[0])
	}
}

func TestSendMessage_BotBlockedWhenNotActive(t *testing.T) {
	chatID := uuid.New()
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: uuid.New(), BotActive: false}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: false}, nil)

	storage.insertErr = postgres.ErrBotSilenced
	_, _, err := svc.SendMessage(context.Background(), BotUserID, chatID, uuid.New(), "silenced")
	var custErr *customErrors.Error
	if !errors.As(err, &custErr) || custErr.GetStatusCode() != 403 {
		t.Fatalf("expected 403 forbidden, got %v", err)
	}
}

func TestSendMessage_BotAllowedWhenActive(t *testing.T) {
	chatID := uuid.New()
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: uuid.New(), BotActive: true}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: false}, nil)

	_, _, err := svc.SendMessage(context.Background(), BotUserID, chatID, uuid.New(), "hello, need help?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if storage.insertIsAdmin || !storage.insertIsBot {
		t.Fatalf("expected bot flags, got admin=%v bot=%v", storage.insertIsAdmin, storage.insertIsBot)
	}
}

func TestSendMessage_ClientMessageIDRequired(t *testing.T) {
	chatID, ownerID := uuid.New(), uuid.New()
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: ownerID}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{}, nil)
	_, _, err := svc.SendMessage(context.Background(), ownerID, chatID, uuid.Nil, "hi")
	var customErr *customErrors.Error
	if !errors.As(err, &customErr) || customErr.GetStatusCode() != 400 {
		t.Fatalf("expected 400, got %v", err)
	}
}

func TestSendMessage_StrangerForbidden(t *testing.T) {
	chatID := uuid.New()
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: uuid.New(), BotActive: true}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: false}, nil)

	_, _, err := svc.SendMessage(context.Background(), uuid.New(), chatID, uuid.New(), "hi")
	var custErr *customErrors.Error
	if !errors.As(err, &custErr) || custErr.GetStatusCode() != 403 {
		t.Fatalf("expected 403 forbidden, got %v", err)
	}
}

func TestSendMessage_EmptyTextRejected(t *testing.T) {
	chatID := uuid.New()
	ownerID := uuid.New()
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: ownerID, BotActive: true}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: false}, nil)

	_, _, err := svc.SendMessage(context.Background(), ownerID, chatID, uuid.New(), "   ")
	var custErr *customErrors.Error
	if !errors.As(err, &custErr) || custErr.GetStatusCode() != 400 {
		t.Fatalf("expected 400 bad request, got %v", err)
	}
}

func TestMarkChatRead_Owner(t *testing.T) {
	chatID := uuid.New()
	ownerID := uuid.New()
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: ownerID}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{}, nil)

	readAt, err := svc.MarkChatRead(context.Background(), ownerID, chatID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if time.Since(readAt) > time.Minute {
		t.Fatalf("expected readAt to be roughly now, got %v", readAt)
	}
}

func TestMarkChatRead_AdminUsesParticipantUpsert(t *testing.T) {
	chatID := uuid.New()
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: uuid.New()}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: true}, nil)

	_, err := svc.MarkChatRead(context.Background(), uuid.New(), chatID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMarkChatRead_Bot(t *testing.T) {
	chatID := uuid.New()
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: uuid.New()}, chatFound: true}
	// fakeAccess.err forces CheckAccess to fail if it is ever invoked - this
	// proves MarkChatRead short-circuits on BotUserID before calling isAdmin,
	// mirroring SendMessage's bot-check-before-isAdmin pattern.
	svc := New(zerolog.Nop(), storage, fakeAccess{err: errors.New("access service should not be called for the bot")}, nil)

	readAt, err := svc.MarkChatRead(context.Background(), BotUserID, chatID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if time.Since(readAt) > time.Minute {
		t.Fatalf("expected readAt to be roughly now, got %v", readAt)
	}
}

func TestMarkChatRead_StrangerForbidden(t *testing.T) {
	chatID := uuid.New()
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: uuid.New()}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: false}, nil)

	_, err := svc.MarkChatRead(context.Background(), uuid.New(), chatID)
	var custErr *customErrors.Error
	if !errors.As(err, &custErr) || custErr.GetStatusCode() != 403 {
		t.Fatalf("expected 403 forbidden, got %v", err)
	}
}

func TestMarkChatRead_MissingChatIsBadRequest(t *testing.T) {
	svc := New(zerolog.Nop(), &fakeStorage{chatFound: false}, fakeAccess{}, nil)
	_, err := svc.MarkChatRead(context.Background(), uuid.New(), uuid.New())
	var customErr *customErrors.Error
	if !errors.As(err, &customErr) || customErr.GetStatusCode() != 400 {
		t.Fatalf("expected 400, got %v", err)
	}
}
