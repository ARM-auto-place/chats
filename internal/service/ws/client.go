package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	chatService "github.com/ARM-auto-place/chats/internal/service"
	"github.com/ARM-auto-place/chats/pkg/models"
)

var (
	WriteWait                  = 10 * time.Second
	PongWait                   = 60 * time.Second
	PingPeriod                 = 30 * time.Second // < PongWait
	MaxMessageSize       int64 = 64 * 1024
	SendBufferSize             = 64
	ReadWorkerCount            = 1
	ReadWorkerBufferSize       = 16
	ReadWorkerMaxRetries       = 3
	ReadWorkerRetryDelay       = 100 * time.Millisecond

	BadJSON        = "bad json"
	BadPayload     = "bad payload"
	UnknownType    = "unknown type"
	ChatIDRequired = "chat_id required"
	Forbidden      = "forbidden"
)

// wsConn is the subset of *websocket.Conn (github.com/gofiber/websocket/v2,
// itself a thin wrapper over gorilla/websocket) the client needs — narrowed
// to keep this file testable without a real network connection (see
// client_test.go's fakeConn).
type wsConn interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	WriteControl(messageType int, data []byte, deadline time.Time) error
	SetReadLimit(limit int64)
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	SetPongHandler(h func(appData string) error)
	SetCloseHandler(h func(code int, text string) error)
	Close() error
}

// Client — одно соединение, у одного пользователя может быть несколько соединений.
type Client struct {
	UserID  uuid.UUID
	IsAdmin bool
	IsBot   bool
	ChatID  uuid.UUID // чат owner-клиента; uuid.Nil для queue-клиента

	conn   wsConn
	send   chan []byte
	hub    *Hub
	svc    chatService.ChatBackend
	logger zerolog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once

	// Пул воркеров, разбирающих входящие WS-сообщения этого клиента.
	// ReadPump только читает кадры с сокета и раскладывает их по каналам
	// round-robin, сама обработка (dispatch) идёт параллельно в workerCount
	// горутинах. wg используется, чтобы дождаться слива буферов при закрытии
	// соединения, прежде чем рвать сокет.
	workChans []chan models.WSIncoming
	workersWg sync.WaitGroup
}

func NewClient(userID uuid.UUID, isAdmin bool, isBot bool, chatID uuid.UUID, conn wsConn, hub *Hub, backend chatService.ChatBackend, logger zerolog.Logger) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		UserID:  userID,
		IsAdmin: isAdmin,
		IsBot:   isBot,
		ChatID:  chatID,
		conn:    conn,
		send:    make(chan []byte, SendBufferSize),
		hub:     hub,
		svc:     backend,
		logger:  logger,
		ctx:     ctx,
		cancel:  cancel,
	}
	c.startWorkers()
	return c
}

func (c *Client) isQueue() bool { return c.IsAdmin || c.IsBot }

func (c *Client) isClosed() bool { return c.ctx.Err() != nil }

// startWorkers поднимает ReadWorkerCount горутин, каждая со своим буферизованным
// каналом (буфер ReadWorkerBufferSize). Оба значения приходят из конфига.
// При ReadWorkerCount > 1 порядок обработки кадров одного клиента не
// гарантирован (два send_message подряд могут сохраниться в обратном порядке),
// поэтому по умолчанию воркер один.
func (c *Client) startWorkers() {
	workerCount := max(ReadWorkerCount, 1)
	bufferSize := max(ReadWorkerBufferSize, 0)

	c.workChans = make([]chan models.WSIncoming, workerCount)
	for i := range workerCount {
		ch := make(chan models.WSIncoming, bufferSize)
		c.workChans[i] = ch

		c.workersWg.Add(1)
		go func(ch <-chan models.WSIncoming) {
			defer c.workersWg.Done()
			for {
				err := c.tick(ch)
				if err == nil {
					continue
				}
				if !errors.Is(err, errWorkerStopped) {
					c.close("read_worker_failed:" + err.Error())
				}
				return
			}
		}(ch)
	}
}

// errWorkerStopped — штатная остановка воркера: канал закрыт или контекст
// клиента отменён. Соединение при этом не закрываем повторно.
var errWorkerStopped = errors.New("worker stopped")

// tick забирает одно сообщение из канала воркера и обрабатывает его,
// делая до ReadWorkerMaxRetries попыток с экспоненциальной паузой между ними.
// Если все попытки упали — возвращает ошибку, и воркер закрывает соединение.
func (c *Client) tick(ch <-chan models.WSIncoming) error {
	var in models.WSIncoming
	select {
	case msg, ok := <-ch:
		if !ok {
			return errWorkerStopped
		}
		in = msg
	case <-c.ctx.Done():
		return errWorkerStopped
	}

	attempts := max(ReadWorkerMaxRetries, 1)
	delay := ReadWorkerRetryDelay
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err = c.safeDispatch(in); err == nil {
			return nil
		}
		c.logger.Warn().Err(err).Int("attempt", attempt).Str("type", string(in.Type)).Str("chatID", c.ChatID.String()).Str("userID", c.UserID.String()).Str("event", "ws_dispatch_failed").Msg("chat websocket dispatch failed")

		if attempt == attempts {
			break
		}
		select {
		case <-time.After(delay):
			delay *= 2
		case <-c.ctx.Done():
			return errWorkerStopped
		}
	}
	return fmt.Errorf("dispatch %s after %d attempts: %w", in.Type, attempts, err)
}

// safeDispatch превращает панику обработчика в ошибку, чтобы одна кривая
// обработка не роняла весь процесс.
func (c *Client) safeDispatch(in models.WSIncoming) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	c.dispatch(in)
	return nil
}

// stopWorkers закрывает каналы воркеров и ждёт, пока они дообработают уже
// принятые сообщения, прежде чем соединение будет закрыто.
func (c *Client) stopWorkers() {
	for _, ch := range c.workChans {
		close(ch)
	}
	c.workersWg.Wait()
}

// ReadPump читает WS-сообщения и раскладывает их round-robin по каналам
// воркеров (startWorkers). Сама горутина ReadPump никогда не блокируется на
// обработке — только на чтении сокета и на отправке во внутренний канал,
// поэтому один медленный/долгий handler не тормозит чтение следующих кадров.
func (c *Client) ReadPump() {
	reason := "read_pump_stopped"
	defer func() {
		c.stopWorkers()
		c.close(reason)
	}()

	c.conn.SetReadLimit(MaxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(PongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(PongWait))
	})
	c.conn.SetCloseHandler(func(code int, text string) error {
		reason = fmt.Sprintf("client_close_frame:code=%d", code)
		_ = c.conn.WriteControl(websocket.CloseMessage, []byte{}, time.Now().Add(WriteWait))
		return nil
	})

	var next int
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if !strings.HasPrefix(reason, "client_close_frame") {
				reason = "read_error:" + err.Error()
			}
			return
		}

		var in models.WSIncoming
		if err = json.Unmarshal(data, &in); err != nil {
			c.sendJSON(models.WSOutgoing{Type: models.WSTypeError, Error: BadJSON})
			continue
		}

		select {
		case c.workChans[next] <- in:
		case <-c.ctx.Done():
			return
		}
		next = (next + 1) % len(c.workChans)
	}
}

// dispatch обрабатывает одно входящее WS-сообщение. Вызывается из воркеров
// (startWorkers), может выполняться параллельно с другими вызовами dispatch
// этого же клиента — обработчики не должны полагаться на порядок между
// разными типами сообщений.
func (c *Client) dispatch(in models.WSIncoming) {
	switch in.Type {
	case models.WSTypePing:
		c.sendJSON(models.WSOutgoing{Type: models.WSTypePong})
	case models.WSTypeSendMessage:
		c.handleSendMessage(in)
	case models.WSTypeRead:
		c.handleRead(in)
	case models.WSTypeReplay:
		c.handleReplay(in)
	default:
		c.sendJSON(models.WSOutgoing{Type: models.WSTypeError, Error: UnknownType})
	}
}

func (c *Client) handleSendMessage(in models.WSIncoming) {
	if in.ChatID == uuid.Nil {
		c.sendJSON(models.WSOutgoing{Type: models.WSTypeError, Error: ChatIDRequired})
		return
	}
	var payload models.SendMessagePayload
	if err := json.Unmarshal(in.Payload, &payload); err != nil || strings.TrimSpace(payload.Text) == "" || payload.ClientMsgID == uuid.Nil {
		c.sendJSON(models.WSOutgoing{Type: models.WSTypeError, ChatID: in.ChatID, Error: BadPayload})
		return
	}

	msg, created, err := c.svc.SendMessage(c.ctx, c.UserID, in.ChatID, payload.ClientMsgID, payload.Text)
	if err != nil {
		c.sendJSON(models.WSOutgoing{Type: models.WSTypeError, ChatID: in.ChatID, Error: err.Error()})
		return
	}

	// ACK отправителю
	c.sendJSON(models.WSOutgoing{
		Type:    models.WSTypeMessageAck,
		ChatID:  in.ChatID,
		Message: &msg,
		Meta:    map[string]uuid.UUID{"client_msg_id": payload.ClientMsgID},
	})

	if !created {
		return
	}
	data, err := json.Marshal(models.WSOutgoing{Type: models.WSTypeMessage, ChatID: in.ChatID, Message: &msg})
	if err != nil {
		c.logger.Error().Err(err).Str("chatID", in.ChatID.String()).Str("userID", c.UserID.String()).Str("event", "ws_message_marshal").Msg("failed to marshal websocket message")
		return
	}
	c.broadcast(chatEvent{chatID: in.ChatID, data: data, exclude: c, event: "message"})
}

func (c *Client) handleRead(in models.WSIncoming) {
	if in.ChatID == uuid.Nil {
		c.sendJSON(models.WSOutgoing{Type: models.WSTypeError, Error: ChatIDRequired})
		return
	}
	readAt, err := c.svc.MarkChatRead(c.ctx, c.UserID, in.ChatID)
	if err != nil {
		c.sendJSON(models.WSOutgoing{Type: models.WSTypeError, ChatID: in.ChatID, Error: err.Error()})
		return
	}
	data, err := json.Marshal(models.WSOutgoing{
		Type:   models.WSTypeMessageRead,
		ChatID: in.ChatID,
		Meta:   map[string]interface{}{"user_id": c.UserID, "read_at": readAt},
	})
	if err != nil {
		c.logger.Error().Err(err).Str("chatID", in.ChatID.String()).Str("userID", c.UserID.String()).Str("event", "ws_read_marshal").Msg("failed to marshal websocket read receipt")
		return
	}
	c.broadcast(chatEvent{chatID: in.ChatID, data: data, event: "message_read"})
}

// handleReplay досылает кадры чата с seq > in.LastSeq из replay-буфера хаба.
// Owner может запросить только свой чат (chatId можно не передавать),
// queue-клиент — любой.
func (c *Client) handleReplay(in models.WSIncoming) {
	chatID := in.ChatID
	if !c.isQueue() {
		if chatID == uuid.Nil {
			chatID = c.ChatID
		}
		if chatID != c.ChatID {
			c.sendJSON(models.WSOutgoing{Type: models.WSTypeError, ChatID: in.ChatID, Error: Forbidden})
			return
		}
	} else if chatID == uuid.Nil {
		c.sendJSON(models.WSOutgoing{Type: models.WSTypeError, Error: ChatIDRequired})
		return
	}
	if !c.hub.allowed(c.ctx, c, chatID, "replay") {
		return
	}

	// Буфер replay (до replaySize кадров) больше send-буфера, поэтому здесь
	// ждём, пока WritePump разгребёт очередь, а не отключаем как slow consumer.
	for _, data := range c.hub.ReplaySince(chatID, in.LastSeq) {
		select {
		case c.send <- data:
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Client) broadcast(evt chatEvent) {
	if err := c.hub.enqueue(c.ctx, evt); err != nil {
		c.logger.Warn().Err(err).Str("chatID", evt.chatID.String()).Str("userID", c.UserID.String()).Str("event", "ws_broadcast_failed").Str("sourceEvent", evt.event).Msg("failed to enqueue chat broadcast")
	}
}

// WritePump drains the send channel to the socket and pings periodically to
// keep the connection alive through idle proxies (the pong resets the read
// deadline in ReadPump). The only goroutine that writes data frames.
func (c *Client) WritePump() {
	ticker := time.NewTicker(PingPeriod)
	reason := "write_pump_stopped"
	defer func() {
		ticker.Stop()
		c.close(reason)
	}()

	for {
		select {
		case <-c.ctx.Done():
			reason = "context_done"
			return
		case data := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(WriteWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				reason = "write_error:" + err.Error()
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(WriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				reason = "ping_write_error:" + err.Error()
				return
			}
		}
	}
}

// trySend enqueues data without blocking; a client whose buffer is full is
// dropped rather than stalling whoever is broadcasting to it.
func (c *Client) trySend(data []byte) {
	select {
	case <-c.ctx.Done():
		return
	default:
	}
	select {
	case c.send <- data:
	case <-c.ctx.Done():
	default:
		c.close("slow_consumer:send_buffer_full")
	}
}

// sendJSON enqueues a reply addressed back to this client itself (pong,
// ack, error frames) onto the same send channel broadcasts use, so WritePump
// stays the single goroutine that ever writes data frames to the socket.
func (c *Client) sendJSON(frame models.WSOutgoing) {
	data, err := json.Marshal(frame)
	if err != nil {
		c.logger.Error().Err(err).Str("chatID", c.ChatID.String()).Str("userID", c.UserID.String()).Str("event", "ws_marshal").Msg("failed to marshal websocket frame")
		return
	}
	c.trySend(data)
}

// close is idempotent: safe to call from ReadPump, WritePump, workers, the
// hub's shards or CloseAll.
func (c *Client) close(reason string) {
	c.once.Do(func() {
		c.logger.Info().Str("reason", reason).Str("chatID", c.ChatID.String()).Str("userID", c.UserID.String()).Bool("isAdmin", c.IsAdmin).Bool("isBot", c.IsBot).Str("event", "ws_closed").Msg("chat websocket connection closed")
		c.cancel()
		select {
		case c.hub.unregister <- c:
		case <-c.hub.done:
		}
		_ = c.conn.Close()
	})
}
