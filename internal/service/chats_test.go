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
	internalModels "github.com/ARM-auto-place/chats/internal/models"
	"github.com/ARM-auto-place/chats/pkg/models"
)

// fakeStorage is a minimal in-memory Storage fake shared by this file's
// tests. Only the fields/methods a given test needs are populated.
type fakeStorage struct {
	chat              internalModels.ChatRow
	chatFound         bool
	queueRows         []internalModels.ChatListRow
	queueTotal        int
	messages          []internalModels.MessageRow
	messagesNext      bool
	messageCreated    bool
	insertIsAdmin     bool
	insertIsBot       bool
	insertClientMsgID uuid.UUID
	listCursor        *internalModels.MessageCursor
	listOffset        int
	err               error
	insertErr         error
	setBotActiveCalls []bool
	systemMessages    []string
	systemMessageErr  error
}

func (f *fakeStorage) GetOrCreateChatByOwner(context.Context, uuid.UUID) (internalModels.ChatRow, error) {
	return f.chat, f.err
}
func (f *fakeStorage) GetChatByID(context.Context, uuid.UUID) (internalModels.ChatRow, bool, error) {
	return f.chat, f.chatFound, f.err
}
func (f *fakeStorage) ListQueue(_ context.Context, _ uuid.UUID, _ int, offset int) ([]internalModels.ChatListRow, int, error) {
	f.listOffset = offset
	return f.queueRows, f.queueTotal, f.err
}
func (f *fakeStorage) SetBotActive(_ context.Context, _ uuid.UUID, active bool) error {
	f.setBotActiveCalls = append(f.setBotActiveCalls, active)
	return f.err
}
func (f *fakeStorage) MarkUserRead(context.Context, uuid.UUID) error { return f.err }
func (f *fakeStorage) UpsertParticipantRead(context.Context, uuid.UUID, uuid.UUID) error {
	return f.err
}
func (f *fakeStorage) InsertMessage(_ context.Context, _ uuid.UUID, _ uuid.UUID, clientMsgID uuid.UUID, _ string, isAdmin bool, isBot bool) (internalModels.MessageRow, bool, error) {
	f.insertClientMsgID = clientMsgID
	f.insertIsAdmin = isAdmin
	f.insertIsBot = isBot
	return internalModels.MessageRow{}, f.messageCreated, f.insertErr
}
func (f *fakeStorage) ListMessages(_ context.Context, _ uuid.UUID, cursor *internalModels.MessageCursor, _ int) ([]internalModels.MessageRow, bool, error) {
	f.listCursor = cursor
	return f.messages, f.messagesNext, f.err
}
func (f *fakeStorage) InsertSystemMessage(_ context.Context, _ uuid.UUID, _ uuid.UUID, text string) (internalModels.MessageRow, error) {
	f.systemMessages = append(f.systemMessages, text)
	return internalModels.MessageRow{Text: text, IsSystem: true}, f.systemMessageErr
}

func TestListSupportQueue_NegativeOffsetRejected(t *testing.T) {
	storage := &fakeStorage{}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: true}, nil)
	_, err := svc.ListSupportQueue(context.Background(), uuid.New(), 20, -1)
	var customErr *customErrors.Error
	if !errors.As(err, &customErr) || customErr.GetStatusCode() != 400 {
		t.Fatalf("expected 400, got %v", err)
	}
}

func TestGetMessages_CompositeCursor(t *testing.T) {
	chatID, ownerID, cursorID := uuid.New(), uuid.New(), uuid.New()
	storage := &fakeStorage{chat: internalModels.ChatRow{ID: chatID, UserID: ownerID}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{}, nil)
	timestamp := time.Now().UTC().Truncate(time.Microsecond)
	_, err := svc.GetMessages(context.Background(), ownerID, chatID, timestamp.Format(time.RFC3339Nano)+","+cursorID.String(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if storage.listCursor == nil || !storage.listCursor.HasID || storage.listCursor.ID != cursorID || !storage.listCursor.SentAt.Equal(timestamp) {
		t.Fatalf("unexpected cursor: %+v", storage.listCursor)
	}
}

type fakeAccess struct {
	allow bool
	err   error
}

func (f fakeAccess) CheckAccess(context.Context, uuid.UUID, int) (bool, error) {
	return f.allow, f.err
}

func TestGetOrCreateSupportChat_ReturnsMappedChat(t *testing.T) {
	chatID := uuid.New()
	ownerID := uuid.New()
	storage := &fakeStorage{chat: internalModels.ChatRow{ID: chatID, UserID: ownerID, BotActive: true, CreatedAt: time.Now()}}
	svc := New(zerolog.Nop(), storage, fakeAccess{}, nil)

	got, err := svc.GetOrCreateSupportChat(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != chatID || got.UserID != ownerID || !got.BotActive {
		t.Fatalf("unexpected chat: %+v", got)
	}
}

func TestGetOrCreateSupportChat_MissingIdentityUnauthorized(t *testing.T) {
	svc := New(zerolog.Nop(), &fakeStorage{}, fakeAccess{}, nil)
	_, err := svc.GetOrCreateSupportChat(context.Background(), uuid.Nil)
	var customErr *customErrors.Error
	if !errors.As(err, &customErr) || customErr.GetStatusCode() != 401 {
		t.Fatalf("expected 401, got %v", err)
	}
}

func TestListSupportQueue_NonAdminForbidden(t *testing.T) {
	storage := &fakeStorage{}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: false}, nil)

	_, err := svc.ListSupportQueue(context.Background(), uuid.New(), 20, 0)
	var custErr *customErrors.Error
	if !errors.As(err, &custErr) || custErr.GetStatusCode() != 403 {
		t.Fatalf("expected 403 forbidden, got %v", err)
	}
}

func TestListSupportQueue_AdminAllowed_ComputesHasNext(t *testing.T) {
	storage := &fakeStorage{
		queueRows:  []internalModels.ChatListRow{{ChatRow: internalModels.ChatRow{ID: uuid.New()}}},
		queueTotal: 5,
	}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: true}, nil)

	resp, err := svc.ListSupportQueue(context.Background(), uuid.New(), 1, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Chats) != 1 {
		t.Fatalf("expected 1 chat, got %d", len(resp.Chats))
	}
	if !resp.HasNext {
		t.Fatalf("expected HasNext = true (offset 0 + limit 1 < total 5)")
	}
}

func TestGetMessages_OwnerAllowed_ChronologicalOrder(t *testing.T) {
	chatID := uuid.New()
	ownerID := uuid.New()
	newer := time.Now()
	older := newer.Add(-time.Minute)
	storage := &fakeStorage{
		chat:      internalModels.ChatRow{ID: chatID, UserID: ownerID},
		chatFound: true,
		// storage returns newest-first, as documented on ListMessages.
		messages: []internalModels.MessageRow{
			{ID: uuid.New(), Text: "second", SentAt: newer},
			{ID: uuid.New(), Text: "first", SentAt: older},
		},
	}
	svc := New(zerolog.Nop(), storage, fakeAccess{}, nil)

	resp, err := svc.GetMessages(context.Background(), ownerID, chatID, "", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Messages) != 2 || resp.Messages[0].Text != "first" || resp.Messages[1].Text != "second" {
		t.Fatalf("expected chronological order [first, second], got %+v", resp.Messages)
	}
}

func TestGetMessages_NonOwnerNonAdminForbidden(t *testing.T) {
	chatID := uuid.New()
	storage := &fakeStorage{chat: internalModels.ChatRow{ID: chatID, UserID: uuid.New()}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: false}, nil)

	_, err := svc.GetMessages(context.Background(), uuid.New(), chatID, "", 10)
	var custErr *customErrors.Error
	if !errors.As(err, &custErr) || custErr.GetStatusCode() != 403 {
		t.Fatalf("expected 403 forbidden, got %v", err)
	}
}

func TestGetMessages_ChatNotFound(t *testing.T) {
	storage := &fakeStorage{chatFound: false}
	svc := New(zerolog.Nop(), storage, fakeAccess{}, nil)

	_, err := svc.GetMessages(context.Background(), uuid.New(), uuid.New(), "", 10)
	var custErr *customErrors.Error
	if !errors.As(err, &custErr) || custErr.GetStatusCode() != 400 {
		t.Fatalf("expected 400 bad request, got %v", err)
	}
}

func TestGetMessages_InvalidCursorRejectedBeforeStorage(t *testing.T) {
	chatID, ownerID := uuid.New(), uuid.New()
	storage := &fakeStorage{chat: internalModels.ChatRow{ID: chatID, UserID: ownerID}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{}, nil)
	_, err := svc.GetMessages(context.Background(), ownerID, chatID, "broken", 10)
	var customErr *customErrors.Error
	if !errors.As(err, &customErr) || customErr.GetStatusCode() != 400 {
		t.Fatalf("expected 400, got %v", err)
	}
}

func TestSetChatStage_OwnerRequestsOperator(t *testing.T) {
	chatID, ownerID := uuid.New(), uuid.New()
	storage := &fakeStorage{chat: internalModels.ChatRow{ID: chatID, UserID: ownerID, BotActive: true}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: false}, nil)

	got, err := svc.SetChatStage(context.Background(), ownerID, chatID, StageOperator)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.BotActive {
		t.Fatalf("expected bot_active=false in response, got %+v", got)
	}
	if len(storage.setBotActiveCalls) != 1 || storage.setBotActiveCalls[0] != false {
		t.Fatalf("expected one SetBotActive(false) call, got %v", storage.setBotActiveCalls)
	}
	if len(storage.systemMessages) != 1 {
		t.Fatalf("expected one system message, got %v", storage.systemMessages)
	}
}

func TestSetChatStage_AdminReturnsToBot(t *testing.T) {
	chatID := uuid.New()
	storage := &fakeStorage{chat: internalModels.ChatRow{ID: chatID, UserID: uuid.New(), BotActive: false}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: true}, nil)

	got, err := svc.SetChatStage(context.Background(), uuid.New(), chatID, StageBot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.BotActive {
		t.Fatalf("expected bot_active=true in response, got %+v", got)
	}
	if len(storage.setBotActiveCalls) != 1 || storage.setBotActiveCalls[0] != true {
		t.Fatalf("expected one SetBotActive(true) call, got %v", storage.setBotActiveCalls)
	}
}

func TestSetChatStage_NoopWhenAlreadyInStage(t *testing.T) {
	chatID, ownerID := uuid.New(), uuid.New()
	storage := &fakeStorage{chat: internalModels.ChatRow{ID: chatID, UserID: ownerID, BotActive: false}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{}, nil)

	_, err := svc.SetChatStage(context.Background(), ownerID, chatID, StageOperator)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(storage.setBotActiveCalls) != 0 || len(storage.systemMessages) != 0 {
		t.Fatalf("expected no writes for a no-op stage change, got setBotActive=%v systemMessages=%v", storage.setBotActiveCalls, storage.systemMessages)
	}
}

func TestSetChatStage_InvalidStageRejected(t *testing.T) {
	chatID, ownerID := uuid.New(), uuid.New()
	storage := &fakeStorage{chat: internalModels.ChatRow{ID: chatID, UserID: ownerID}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{}, nil)

	_, err := svc.SetChatStage(context.Background(), ownerID, chatID, "confused")
	var custErr *customErrors.Error
	if !errors.As(err, &custErr) || custErr.GetStatusCode() != 400 {
		t.Fatalf("expected 400 bad request, got %v", err)
	}
}

func TestSetChatStage_StrangerForbidden(t *testing.T) {
	chatID := uuid.New()
	storage := &fakeStorage{chat: internalModels.ChatRow{ID: chatID, UserID: uuid.New(), BotActive: true}, chatFound: true}
	svc := New(zerolog.Nop(), storage, fakeAccess{allow: false}, nil)

	_, err := svc.SetChatStage(context.Background(), uuid.New(), chatID, StageOperator)
	var custErr *customErrors.Error
	if !errors.As(err, &custErr) || custErr.GetStatusCode() != 403 {
		t.Fatalf("expected 403 forbidden, got %v", err)
	}
}

func TestSetChatStage_ChatNotFound(t *testing.T) {
	storage := &fakeStorage{chatFound: false}
	svc := New(zerolog.Nop(), storage, fakeAccess{}, nil)

	_, err := svc.SetChatStage(context.Background(), uuid.New(), uuid.New(), StageOperator)
	var custErr *customErrors.Error
	if !errors.As(err, &custErr) || custErr.GetStatusCode() != 400 {
		t.Fatalf("expected 400 bad request, got %v", err)
	}
}

func TestSetChatStage_BroadcastsStageChangedToSubscribers(t *testing.T) {
	chatID, ownerID := uuid.New(), uuid.New()
	storage := &fakeStorage{chat: internalModels.ChatRow{ID: chatID, UserID: ownerID, BotActive: true}, chatFound: true}
	hub := &fakeBroadcaster{}
	svc := New(zerolog.Nop(), storage, fakeAccess{}, hub)

	if _, err := svc.SetChatStage(context.Background(), ownerID, chatID, StageOperator); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

// fakeBroadcaster records every frame handed to Broadcast.
type fakeBroadcaster struct {
	chatIDs []uuid.UUID
	frames  [][]byte
}

func (f *fakeBroadcaster) Broadcast(_ context.Context, chatID uuid.UUID, data []byte, _ string) error {
	f.chatIDs = append(f.chatIDs, chatID)
	f.frames = append(f.frames, data)
	return nil
}
