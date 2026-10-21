package ws

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/ARM-auto-place/chats/pkg/models"
)

const waitTimeout = time.Second

// fakeConn is a minimal wsConn fake: ReadMessage returns queued frames in
// order, then io.EOF; WriteMessage records what was written.
type fakeConn struct {
	mu       sync.Mutex
	toRead   [][]byte
	readPos  int
	written  [][]byte
	closed   bool
	writeErr error
}

func (f *fakeConn) ReadMessage() (int, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readPos >= len(f.toRead) {
		return 0, nil, io.EOF
	}
	data := f.toRead[f.readPos]
	f.readPos++
	return 1, data, nil
}

func (f *fakeConn) WriteMessage(_ int, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.written = append(f.written, data)
	return nil
}

func (f *fakeConn) WriteControl(int, []byte, time.Time) error { return nil }
func (f *fakeConn) SetReadLimit(int64)                        {}
func (f *fakeConn) SetReadDeadline(time.Time) error           { return nil }
func (f *fakeConn) SetWriteDeadline(time.Time) error          { return nil }
func (f *fakeConn) SetPongHandler(func(string) error)         {}
func (f *fakeConn) SetCloseHandler(func(int, string) error)   {}

func (f *fakeConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeConn) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

type fakeBackend struct {
	sendMessageResult  models.Message
	sendMessageErr     error
	sendMessageCreated bool
	markReadResult     time.Time
	markReadErr        error
}

func (f *fakeBackend) EnsureOwnerChat(context.Context, uuid.UUID) (models.Chat, error) {
	return models.Chat{}, nil
}
func (f *fakeBackend) SendMessage(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string) (models.Message, bool, error) {
	return f.sendMessageResult, f.sendMessageCreated, f.sendMessageErr
}
func (f *fakeBackend) MarkChatRead(context.Context, uuid.UUID, uuid.UUID) (time.Time, error) {
	return f.markReadResult, f.markReadErr
}

type fakeAccess struct {
	allow bool
	err   error
}

func (f fakeAccess) CheckAccess(context.Context, uuid.UUID, int) (bool, error) {
	return f.allow, f.err
}

// startHub runs a hub for the test's lifetime.
func startHub(t *testing.T, access *fakeAccess) *Hub {
	t.Helper()
	var hub *Hub
	if access != nil {
		hub = NewHub(*access, zerolog.Nop())
	} else {
		hub = NewHub(nil, zerolog.Nop())
	}
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	t.Cleanup(func() {
		cancel()
		<-hub.done
	})
	return hub
}

func newTestClient(hub *Hub, conn wsConn, backend *fakeBackend, isAdmin bool, chatID uuid.UUID) *Client {
	if backend == nil {
		backend = &fakeBackend{}
	}
	return NewClient(uuid.New(), isAdmin, false, chatID, conn, hub, backend, zerolog.Nop())
}

// register sends c through hub.Register and waits until the coordinator has
// applied it.
func register(t *testing.T, hub *Hub, c *Client, chatID uuid.UUID) {
	t.Helper()
	hub.Register <- c
	eventually(t, func() bool {
		for _, s := range hub.Subscribers(chatID) {
			if s == c {
				return true
			}
		}
		return false
	}, "client was not registered")
}

func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}

// recv waits for the next frame queued on c.send.
func recv(t *testing.T, c *Client) models.WSOutgoing {
	t.Helper()
	select {
	case data := <-c.send:
		var out models.WSOutgoing
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("failed to decode frame %s: %v", data, err)
		}
		return out
	case <-time.After(waitTimeout):
		t.Fatal("expected a frame on client.send")
		return models.WSOutgoing{}
	}
}

// assertNoFrame checks nothing arrives on c.send within a short window.
func assertNoFrame(t *testing.T, c *Client) {
	t.Helper()
	select {
	case data := <-c.send:
		t.Fatalf("expected no frame, got %s", data)
	case <-time.After(50 * time.Millisecond):
	}
}

func encodeFrame(t *testing.T, in models.WSIncoming) []byte {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return data
}

func stageFrame(t *testing.T, chatID uuid.UUID) []byte {
	t.Helper()
	data, err := json.Marshal(models.WSOutgoing{Type: models.WSTypeStageChanged, ChatID: chatID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return data
}
