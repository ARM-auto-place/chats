package ws

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ARM-auto-place/chats/pkg/models"
)

func TestClient_Ping_RepliesPong(t *testing.T) {
	hub := startHub(t, nil)
	conn := &fakeConn{toRead: [][]byte{encodeFrame(t, models.WSIncoming{Type: models.WSTypePing})}}
	client := newTestClient(hub, conn, nil, false, uuid.New())

	client.ReadPump()

	// sendJSON enqueues onto client.send rather than writing conn directly
	// (WritePump, not running in this test, is the only writer).
	if out := recv(t, client); out.Type != models.WSTypePong {
		t.Fatalf("expected pong, got %s", out.Type)
	}
	if len(conn.written) != 0 {
		t.Fatalf("expected no direct conn writes (WritePump not running), got %d", len(conn.written))
	}
}

func TestClient_BadJSON_SendsErrorFrame(t *testing.T) {
	hub := startHub(t, nil)
	conn := &fakeConn{toRead: [][]byte{[]byte("{not json")}}
	client := newTestClient(hub, conn, nil, false, uuid.New())

	client.ReadPump()

	if out := recv(t, client); out.Type != models.WSTypeError || out.Error != BadJSON {
		t.Fatalf("expected bad json error, got %+v", out)
	}
}

func TestClient_ReadPumpEOF_ClosesAndUnregisters(t *testing.T) {
	hub := startHub(t, nil)
	chatID := uuid.New()
	conn := &fakeConn{}
	client := newTestClient(hub, conn, nil, false, chatID)
	register(t, hub, client, chatID)

	client.ReadPump()

	if !conn.isClosed() || !client.isClosed() {
		t.Fatal("read error must close the client")
	}
	eventually(t, func() bool { return len(hub.Subscribers(chatID)) == 0 }, "closed client must be unregistered")
}

func TestClient_SendMessage_AcksSenderAndBroadcasts(t *testing.T) {
	hub := startHub(t, nil)
	chatID := uuid.New()
	payload, _ := json.Marshal(models.SendMessagePayload{ClientMsgID: uuid.New(), Text: "hi"})
	conn := &fakeConn{toRead: [][]byte{encodeFrame(t, models.WSIncoming{Type: models.WSTypeSendMessage, ChatID: chatID, Payload: payload})}}
	backend := &fakeBackend{sendMessageResult: models.Message{ID: uuid.New(), ChatID: chatID, Text: "hi"}, sendMessageCreated: true}
	sender := newTestClient(hub, conn, backend, false, chatID)
	register(t, hub, sender, chatID)
	other := newTestClient(hub, &fakeConn{}, backend, true, uuid.Nil)
	register(t, hub, other, chatID)

	sender.ReadPump()

	if ack := recv(t, sender); ack.Type != models.WSTypeMessageAck || ack.Seq != 0 {
		t.Fatalf("expected message_ack without seq, got %+v", ack)
	}
	msg := recv(t, other)
	if msg.Type != models.WSTypeMessage || msg.Seq != 1 {
		t.Fatalf("expected message frame with seq 1, got %+v", msg)
	}
	// the sending connection got the ack, it must not get its own broadcast
	assertNoFrame(t, sender)
}

func TestClient_SendMessage_DuplicateOnlyAcks(t *testing.T) {
	hub := startHub(t, nil)
	chatID := uuid.New()
	payload, _ := json.Marshal(models.SendMessagePayload{ClientMsgID: uuid.New(), Text: "hi"})
	conn := &fakeConn{toRead: [][]byte{encodeFrame(t, models.WSIncoming{Type: models.WSTypeSendMessage, ChatID: chatID, Payload: payload})}}
	backend := &fakeBackend{sendMessageResult: models.Message{ID: uuid.New(), ChatID: chatID}, sendMessageCreated: false}
	sender := newTestClient(hub, conn, backend, false, chatID)
	other := newTestClient(hub, &fakeConn{}, backend, true, uuid.Nil)
	register(t, hub, sender, chatID)
	register(t, hub, other, chatID)

	sender.ReadPump()

	if out := recv(t, sender); out.Type != models.WSTypeMessageAck {
		t.Fatalf("expected ack, got %s", out.Type)
	}
	assertNoFrame(t, other)
}

func TestClient_SendMessage_BackendErrorSendsErrorFrame(t *testing.T) {
	hub := startHub(t, nil)
	chatID := uuid.New()
	payload, _ := json.Marshal(models.SendMessagePayload{ClientMsgID: uuid.New(), Text: "hi"})
	conn := &fakeConn{toRead: [][]byte{encodeFrame(t, models.WSIncoming{Type: models.WSTypeSendMessage, ChatID: chatID, Payload: payload})}}
	client := newTestClient(hub, conn, &fakeBackend{sendMessageErr: errors.New("forbidden")}, false, chatID)

	client.ReadPump()

	if out := recv(t, client); out.Type != models.WSTypeError {
		t.Fatalf("expected error frame, got %s", out.Type)
	}
}

func TestClient_Read_BroadcastsMessageRead(t *testing.T) {
	hub := startHub(t, nil)
	chatID := uuid.New()
	conn := &fakeConn{toRead: [][]byte{encodeFrame(t, models.WSIncoming{Type: models.WSTypeRead, ChatID: chatID})}}
	reader := newTestClient(hub, conn, &fakeBackend{markReadResult: time.Now()}, false, chatID)
	register(t, hub, reader, chatID)
	// broadcast is async and ReadPump closes the reader on EOF right away,
	// so delivery is checked on a separate subscriber
	observer := newTestClient(hub, &fakeConn{}, nil, true, uuid.Nil)
	register(t, hub, observer, chatID)

	reader.ReadPump()

	if out := recv(t, observer); out.Type != models.WSTypeMessageRead {
		t.Fatalf("expected message_read broadcast, got %s", out.Type)
	}
}

func TestClient_Replay_OwnerGetsMissedFrames(t *testing.T) {
	hub := startHub(t, nil)
	chatID := uuid.New()
	for range 3 {
		if err := hub.Broadcast(t.Context(), chatID, stageFrame(t, chatID), "stage_changed"); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, func() bool { return len(hub.ReplaySince(chatID, 0)) == 3 }, "frames were not buffered")

	// chatId omitted: an owner replays its own chat
	conn := &fakeConn{toRead: [][]byte{encodeFrame(t, models.WSIncoming{Type: models.WSTypeReplay, LastSeq: 1})}}
	owner := newTestClient(hub, conn, nil, false, chatID)

	owner.ReadPump()

	for _, want := range []uint64{2, 3} {
		if out := recv(t, owner); out.Seq != want || out.ChatID != chatID {
			t.Fatalf("expected seq %d of chat %s, got %+v", want, chatID, out)
		}
	}
	assertNoFrame(t, owner)
}

func TestClient_Replay_OwnerCannotReadForeignChat(t *testing.T) {
	hub := startHub(t, nil)
	foreign := uuid.New()
	_ = hub.Broadcast(t.Context(), foreign, stageFrame(t, foreign), "stage_changed")
	eventually(t, func() bool { return len(hub.ReplaySince(foreign, 0)) == 1 }, "frame was not buffered")

	conn := &fakeConn{toRead: [][]byte{encodeFrame(t, models.WSIncoming{Type: models.WSTypeReplay, ChatID: foreign})}}
	owner := newTestClient(hub, conn, nil, false, uuid.New())

	owner.ReadPump()

	if out := recv(t, owner); out.Type != models.WSTypeError || out.Error != Forbidden {
		t.Fatalf("expected forbidden error, got %+v", out)
	}
	assertNoFrame(t, owner)
}

func TestClient_WriteFailureClosesAndUnregisters(t *testing.T) {
	hub := startHub(t, nil)
	chatID := uuid.New()
	conn := &fakeConn{writeErr: errors.New("write failed")}
	client := newTestClient(hub, conn, nil, false, chatID)
	register(t, hub, client, chatID)
	client.send <- []byte("frame")

	client.WritePump()

	if !conn.isClosed() {
		t.Fatal("write failure must close the connection")
	}
	eventually(t, func() bool { return len(hub.Subscribers(chatID)) == 0 }, "write failure must unregister client")
	client.close("again") // idempotent
}

func TestClient_SlowConsumerIsDropped(t *testing.T) {
	hub := startHub(t, nil)
	chatID := uuid.New()
	conn := &fakeConn{}
	client := newTestClient(hub, conn, nil, false, chatID)
	register(t, hub, client, chatID)

	for range cap(client.send) + 1 {
		client.trySend([]byte("x"))
	}

	if !conn.isClosed() {
		t.Fatal("client with a full send buffer must be disconnected")
	}
}

func TestClient_DispatchPanic_ClosesAfterRetries(t *testing.T) {
	hub := startHub(t, nil)
	chatID := uuid.New()
	conn := &fakeConn{toRead: [][]byte{encodeFrame(t, models.WSIncoming{Type: models.WSTypeRead, ChatID: chatID})}}
	// nil backend: MarkChatRead panics on a nil interface
	client := NewClient(uuid.New(), false, false, chatID, conn, hub, nil, hub.logger)

	client.ReadPump()

	if !conn.isClosed() {
		t.Fatal("expected the connection to be closed after the handler kept panicking")
	}
}
