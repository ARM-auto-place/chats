package transport

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestGeneratedClientDecodesAMCDataEnvelope(t *testing.T) {
	chatID := uuid.New()
	ownerID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/chat/support":
			_, _ = fmt.Fprintf(w, `{"data":{"id":%q,"userId":%q,"dialogStartedAt":null,"botActive":true,"createdAt":"2026-09-14T10:00:00Z"},"error":false,"errorText":"","additionalErrors":{}}`, chatID, ownerID)
		case "/api/v1/chat/queue":
			_, _ = fmt.Fprint(w, `{"data":{"chats":[],"hasNext":false},"error":false,"errorText":"","additionalErrors":{}}`)
		case "/api/v1/chat/messages":
			_, _ = fmt.Fprint(w, `{"data":{"messages":[],"hasNext":true},"error":false,"errorText":"","additionalErrors":{}}`)
		case "/api/v1/chat/read":
			_, _ = fmt.Fprint(w, `{"data":true,"error":false,"errorText":"","additionalErrors":{}}`)
		}
	}))
	defer server.Close()

	client := NewClientChatAPI(server.URL)
	chat, err := client.GetOrCreateSupportChat(context.Background(), ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if chat.ID != chatID || chat.UserID != ownerID || !chat.BotActive || chat.DialogStartedAt != nil {
		t.Fatalf("generated client decoded the wrong response: %+v", chat)
	}
	queue, err := client.ListSupportQueue(context.Background(), ownerID, 20, 0)
	if err != nil || queue.Chats == nil || queue.HasNext {
		t.Fatalf("queue envelope: response=%+v err=%v", queue, err)
	}
	messages, err := client.GetMessages(context.Background(), ownerID, chatID, "", 30)
	if err != nil || messages.Messages == nil || !messages.HasNext {
		t.Fatalf("messages envelope: response=%+v err=%v", messages, err)
	}
	read, err := client.MarkRead(context.Background(), ownerID, chatID)
	if err != nil || !read {
		t.Fatalf("read envelope: response=%v err=%v", read, err)
	}
}
