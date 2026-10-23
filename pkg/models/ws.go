package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type WSIncomingType string

const (
	WSTypeSendMessage WSIncomingType = "sendNessage"
	WSTypeRead        WSIncomingType = "read"
	WSTypePing        WSIncomingType = "ping"
	// WSTypeReplay asks the server to resend every chat frame with seq >
	// LastSeq still held in the hub's in-memory replay buffer — sent by the
	// frontend right after a reconnect to fill the gap.
	WSTypeReplay WSIncomingType = "replay"
)

// WSIncoming is a client -> server frame. Payload is dispatched further based
// on Type (see SendMessagePayload).
type WSIncoming struct {
	Type    WSIncomingType  `json:"type"`
	ChatID  uuid.UUID       `json:"chatId,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	LastSeq uint64          `json:"lastSeq,omitempty"`
}

type SendMessagePayload struct {
	ClientMsgID uuid.UUID `json:"clientMsgId"`
	Text        string    `json:"text"`
}

type WSOutgoingType string

const (
	WSTypeMessage      WSOutgoingType = "message"
	WSTypeMessageAck   WSOutgoingType = "messageAck"
	WSTypeMessageRead  WSOutgoingType = "messageRead"
	WSTypeStageChanged WSOutgoingType = "stageChanged"
	WSTypePong         WSOutgoingType = "pong"
	WSTypeError        WSOutgoingType = "error"
)

// StageChangedMeta is the Meta payload of a WSTypeStageChanged frame, sent to
// every subscriber of a chat whenever bot_active flips — via the explicit
// POST /v1/chat/stage call or the auto-silence that happens when an Admin
// sends a chat's first message.
type StageChangedMeta struct {
	BotActive bool      `json:"botActive"`
	Stage     string    `json:"stage"`
	ChangedBy uuid.UUID `json:"changedBy"`
	ChangedAt time.Time `json:"changedAt"`
}

// WSOutgoing is a server -> client frame. Seq is set by the hub on every
// frame broadcast to a chat (monotonic per chat, see WSTypeReplay); direct
// replies (ack, pong, error) carry no seq.
type WSOutgoing struct {
	Type    WSOutgoingType `json:"type"`
	ChatID  uuid.UUID      `json:"chatId,omitempty"`
	Message *Message       `json:"message,omitempty"`
	Meta    interface{}    `json:"meta,omitempty"`
	Error   string         `json:"error,omitempty"`
	Seq     uint64         `json:"seq,omitempty"`
}

// MarshalJSON omits chat_id when it's uuid.Nil. The default encoder treats
// uuid.UUID (a [16]byte array) as non-empty even when zero, so the plain
// `omitempty` tag above does not strip the zero UUID on its own — this
// override does the actual work.
func (o WSOutgoing) MarshalJSON() ([]byte, error) {
	wire := struct {
		Type    WSOutgoingType `json:"type"`
		ChatID  string         `json:"chatId,omitempty"`
		Message *Message       `json:"message,omitempty"`
		Meta    interface{}    `json:"meta,omitempty"`
		Error   string         `json:"error,omitempty"`
		Seq     uint64         `json:"seq,omitempty"`
	}{
		Type:    o.Type,
		Message: o.Message,
		Meta:    o.Meta,
		Error:   o.Error,
		Seq:     o.Seq,
	}
	if o.ChatID != uuid.Nil {
		wire.ChatID = o.ChatID.String()
	}
	return json.Marshal(wire)
}
