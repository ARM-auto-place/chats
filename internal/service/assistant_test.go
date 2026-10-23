package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/ARM-auto-place/chats/internal/client"
	"github.com/ARM-auto-place/chats/internal/models"
	pkgModels "github.com/ARM-auto-place/chats/pkg/models"
)

// fakeAssistant records every Chat call and answers with answer/err.
type fakeAssistant struct {
	answer     client.Answer
	err        error
	messages   []string
	sessionIDs []string
}

func (f *fakeAssistant) Chat(_ context.Context, message string, sessionID string) (client.Answer, error) {
	f.messages = append(f.messages, message)
	f.sessionIDs = append(f.sessionIDs, sessionID)
	return f.answer, f.err
}

func TestSendMessage_OwnerMessage_BotReplies(t *testing.T) {
	chatID, ownerID := uuid.New(), uuid.New()
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: ownerID, BotActive: true}, chatFound: true, messageCreated: true}
	hub := &fakeBroadcaster{}
	bot := &fakeAssistant{answer: client.Answer{Text: "ответ бота", TypeID: client.AnswerTypeAI}}
	svc := New(zerolog.Nop(), storage, fakeAccess{}, hub).WithAssistant(bot, time.Second)

	if _, _, err := svc.SendMessage(context.Background(), ownerID, chatID, uuid.New(), "  вопрос  "); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	svc.botReplies.Wait()

	if len(bot.messages) != 1 || bot.messages[0] != "вопрос" || bot.sessionIDs[0] != chatID.String() {
		t.Fatalf("unexpected assistant calls: %v %v", bot.messages, bot.sessionIDs)
	}
	if !storage.insertIsBot {
		t.Fatal("expected bot reply to be persisted as the bot")
	}
	if len(hub.frames) != 1 || hub.chatIDs[0] != chatID {
		t.Fatalf("expected one broadcast of the bot reply, got %d", len(hub.frames))
	}
	var out pkgModels.WSOutgoing
	if err := json.Unmarshal(hub.frames[0], &out); err != nil {
		t.Fatalf("failed to decode broadcast: %v", err)
	}
	if out.Type != pkgModels.WSTypeMessage || out.ChatID != chatID {
		t.Fatalf("unexpected frame: %+v", out)
	}
}

func TestSendMessage_BotInactive_AssistantNotCalled(t *testing.T) {
	chatID, ownerID := uuid.New(), uuid.New()
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: ownerID, BotActive: false}, chatFound: true, messageCreated: true}
	bot := &fakeAssistant{answer: client.Answer{Text: "ответ"}}
	svc := New(zerolog.Nop(), storage, fakeAccess{}, &fakeBroadcaster{}).WithAssistant(bot, time.Second)

	if _, _, err := svc.SendMessage(context.Background(), ownerID, chatID, uuid.New(), "вопрос"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	svc.botReplies.Wait()

	if len(bot.messages) != 0 {
		t.Fatalf("expected no assistant call, got %v", bot.messages)
	}
}

func TestSendMessage_DuplicateMessage_AssistantNotCalled(t *testing.T) {
	chatID, ownerID := uuid.New(), uuid.New()
	// messageCreated=false: the same clientMsgID was already stored.
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: ownerID, BotActive: true}, chatFound: true}
	bot := &fakeAssistant{answer: client.Answer{Text: "ответ"}}
	svc := New(zerolog.Nop(), storage, fakeAccess{}, &fakeBroadcaster{}).WithAssistant(bot, time.Second)

	if _, _, err := svc.SendMessage(context.Background(), ownerID, chatID, uuid.New(), "вопрос"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	svc.botReplies.Wait()

	if len(bot.messages) != 0 {
		t.Fatalf("expected no assistant call, got %v", bot.messages)
	}
}

func TestSendMessage_AssistantError_NoReply(t *testing.T) {
	chatID, ownerID := uuid.New(), uuid.New()
	storage := &fakeStorage{chat: models.ChatRow{ID: chatID, UserID: ownerID, BotActive: true}, chatFound: true, messageCreated: true}
	hub := &fakeBroadcaster{}
	bot := &fakeAssistant{err: errors.New("bot down")}
	svc := New(zerolog.Nop(), storage, fakeAccess{}, hub).WithAssistant(bot, time.Second)

	if _, _, err := svc.SendMessage(context.Background(), ownerID, chatID, uuid.New(), "вопрос"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	svc.botReplies.Wait()

	if storage.insertIsBot || len(hub.frames) != 0 {
		t.Fatalf("expected no bot reply, got insertIsBot=%v frames=%d", storage.insertIsBot, len(hub.frames))
	}
}
