package models

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestWSOutgoing_MarshalJSON_OmitsNilChatID(t *testing.T) {
	out := WSOutgoing{Type: WSTypePong}

	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded map[string]interface{}
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, present := decoded["chatId"]; present {
		t.Fatalf("expected chatId to be omitted, got %s", data)
	}
}

func TestWSOutgoing_MarshalJSON_IncludesChatID(t *testing.T) {
	chatID := uuid.New()
	out := WSOutgoing{Type: WSTypeMessage, ChatID: chatID}

	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded map[string]interface{}
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded["chatId"] != chatID.String() {
		t.Fatalf("expected chatId %q, got %v", chatID.String(), decoded["chatId"])
	}
}
