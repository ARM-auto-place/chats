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
	if _, present := decoded["chat_id"]; present {
		t.Fatalf("expected chat_id to be omitted, got %s", data)
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
	if decoded["chat_id"] != chatID.String() {
		t.Fatalf("expected chat_id %q, got %v", chatID.String(), decoded["chat_id"])
	}
}
