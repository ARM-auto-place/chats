package ws

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	chatService "github.com/ARM-auto-place/chats/internal/service"
	"github.com/ARM-auto-place/chats/pkg/models"
)

const (
	replaySize          = 100
	replayIdleTTL       = 10 * time.Minute
	replaySweepInterval = time.Minute
	accessCheckTimeout  = 2 * time.Second
)

var (
	RegisterBuf   = 256
	UnregisterBuf = 256
	BroadcastBuf  = 1024
	ShardCount    = 8

	errHubStopped = errors.New("ws hub stopped")

	_ chatService.Broadcaster = (*Hub)(nil)
)

// chatEvent — один кадр, который нужно разослать подписчикам чата.
type chatEvent struct {
	chatID  uuid.UUID
	data    []byte
	exclude *Client // соединение-отправитель, ему кадр не дублируем (он получил ack)
	event   string  // только для логов
}

// ringEntry — одна запись в кольцевом буфере
type ringEntry struct {
	seq  uint64
	data []byte
}

// RingBuffer хранит последние replaySize событий чата с монотонным seq.
// buf выделяется лениво и освобождается при простое (evictIdleReplay), а seq
// сохраняется — иначе после очистки нумерация начнётся заново и клиент с
// большим lastSeq отбросит новые кадры как уже виденные.
type RingBuffer struct {
	buf       []ringEntry
	head      int
	size      int
	seq       uint64
	lastWrite time.Time
}

func (rb *RingBuffer) write(data []byte, now time.Time) uint64 {
	if rb.buf == nil {
		rb.buf = make([]ringEntry, replaySize)
	}
	rb.seq++
	rb.buf[rb.head] = ringEntry{seq: rb.seq, data: data}
	rb.head = (rb.head + 1) % replaySize
	if rb.size < replaySize {
		rb.size++
	}
	rb.lastWrite = now
	return rb.seq
}

func (rb *RingBuffer) since(lastSeq uint64) [][]byte {
	start := rb.head - rb.size
	if start < 0 {
		start += replaySize
	}
	var result [][]byte
	for i := 0; i < rb.size; i++ {
		e := rb.buf[(start+i)%replaySize]
		if e.seq > lastSeq {
			result = append(result, e.data)
		}
	}
	return result
}

func (rb *RingBuffer) release() {
	rb.buf = nil
	rb.head = 0
	rb.size = 0
}

// shard обрабатывает broadcast для подмножества chatID: порядок кадров внутри
// одного чата сохраняется, разные чаты рассылаются параллельно.
type shard struct {
	broadcast chan chatEvent
}

func newShard() *shard {
	return &shard{broadcast: make(chan chatEvent, max(BroadcastBuf, 1))}
}

func (s *shard) run(ctx context.Context, h *Hub) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-s.broadcast:
			h.deliver(ctx, evt)
		}
	}
}

// Hub координирует WebSocket-соединения. Соединение — либо "owner" (обычный
// пользователь, видит ровно свой чат), либо "queue" (Admin-оператор или бот,
// видит все чаты сразу). Регистрация идёт через каналы Register/unregister в
// единственную горутину-координатор, рассылка — через N shard-горутин.
type Hub struct {
	Register   chan *Client
	unregister chan *Client
	shards     []*shard

	mu           sync.RWMutex
	chatClients  map[uuid.UUID]map[*Client]struct{} // chatID → owner-клиенты
	queueClients map[*Client]struct{}

	replayMu sync.Mutex
	replay   map[uuid.UUID]*RingBuffer // chatID → ring buffer

	accessClient chatService.AccessClient
	logger       zerolog.Logger

	done chan struct{} // закрывается, когда Run завершился
}

func NewHub(accessClient chatService.AccessClient, logger zerolog.Logger) *Hub {
	shards := make([]*shard, max(ShardCount, 1))
	for i := range shards {
		shards[i] = newShard()
	}
	return &Hub{
		Register:     make(chan *Client, RegisterBuf),
		unregister:   make(chan *Client, UnregisterBuf),
		shards:       shards,
		chatClients:  make(map[uuid.UUID]map[*Client]struct{}),
		queueClients: make(map[*Client]struct{}),
		replay:       make(map[uuid.UUID]*RingBuffer),
		accessClient: accessClient,
		logger:       logger,
		done:         make(chan struct{}),
	}
}

// Run запускает shard-горутины и координатор Register/unregister. Блокирует
// до отмены ctx.
func (h *Hub) Run(ctx context.Context) {
	defer close(h.done)
	for _, s := range h.shards {
		go s.run(ctx, h)
	}
	h.runCoordinator(ctx)
}

func (h *Hub) runCoordinator(ctx context.Context) {
	sweep := time.NewTicker(replaySweepInterval)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-h.Register:
			h.addClient(c)
		case c := <-h.unregister:
			h.removeClient(c)
		case now := <-sweep.C:
			h.evictIdleReplay(now)
		}
	}
}

func (h *Hub) addClient(c *Client) {
	// Register и unregister — разные каналы, select может выбрать unregister
	// раньше. Уже закрытого клиента не регистрируем, иначе он повиснет в мапе.
	if c.isClosed() {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if c.isQueue() {
		h.queueClients[c] = struct{}{}
		return
	}
	if h.chatClients[c.ChatID] == nil {
		h.chatClients[c.ChatID] = make(map[*Client]struct{})
	}
	h.chatClients[c.ChatID][c] = struct{}{}
}

func (h *Hub) removeClient(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if clients, ok := h.chatClients[c.ChatID]; ok {
		delete(clients, c)
		if len(clients) == 0 {
			delete(h.chatClients, c.ChatID)
		}
	}
	delete(h.queueClients, c)
}

func (h *Hub) shardFor(chatID uuid.UUID) *shard {
	idx := binary.BigEndian.Uint16(chatID[:2]) % uint16(len(h.shards))
	return h.shards[idx]
}

// Broadcast рассылает готовый кадр всем подписчикам чата. Реализует
// service.Broadcaster.
func (h *Hub) Broadcast(ctx context.Context, chatID uuid.UUID, data []byte, event string) error {
	return h.enqueue(ctx, chatEvent{chatID: chatID, data: data, event: event})
}

// enqueue маршрутизирует событие в нужный shard по chatID.
func (h *Hub) enqueue(ctx context.Context, evt chatEvent) error {
	select {
	case h.shardFor(evt.chatID).broadcast <- evt:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-h.done:
		return errHubStopped
	}
}

// Subscribers возвращает всех, кто должен получить событие по chatID:
// владельца чата и всех queue-подписчиков. Срез — снимок, безопасен без лока.
func (h *Hub) Subscribers(chatID uuid.UUID) []*Client {
	h.mu.RLock()
	defer h.mu.RUnlock()

	subs := make([]*Client, 0, len(h.chatClients[chatID])+len(h.queueClients))
	for c := range h.chatClients[chatID] {
		subs = append(subs, c)
	}
	for c := range h.queueClients {
		subs = append(subs, c)
	}
	return subs
}

// deliver проставляет кадру seq, кладёт его в replay-буфер и раскладывает по
// send-каналам подписчиков. Вызывается только из shard-горутины этого чата.
func (h *Hub) deliver(ctx context.Context, evt chatEvent) {
	sendData := evt.data
	h.replayMu.Lock()
	rb := h.replay[evt.chatID]
	if rb == nil {
		rb = &RingBuffer{}
		h.replay[evt.chatID] = rb
	}
	if enriched := injectSeq(evt.data, rb.seq+1); enriched != nil {
		sendData = enriched
	}
	rb.write(sendData, time.Now())
	h.replayMu.Unlock()

	for _, c := range h.Subscribers(evt.chatID) {
		if c == evt.exclude {
			continue
		}
		if !h.allowed(ctx, c, evt.chatID, evt.event) {
			continue
		}
		c.trySend(sendData)
	}
}

// allowed перепроверяет роль Admin перед доставкой: доступ могли отозвать уже
// после подключения. Отозванного админа отключаем.
func (h *Hub) allowed(ctx context.Context, c *Client, chatID uuid.UUID, event string) bool {
	if !c.IsAdmin || h.accessClient == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, accessCheckTimeout)
	defer cancel()
	ok, err := h.accessClient.CheckAccess(ctx, c.UserID, chatService.RoleCodeAdmin)
	if err != nil || !ok {
		h.logger.Warn().Err(err).Str("chatID", chatID.String()).Str("userID", c.UserID.String()).Str("event", "ws_access_revoked").Str("sourceEvent", event).Msg("dropping unauthorized chat subscriber")
		c.close("access_revoked")
		return false
	}
	return true
}

// ReplaySince возвращает события чата с seq > lastSeq из in-memory буфера.
func (h *Hub) ReplaySince(chatID uuid.UUID, lastSeq uint64) [][]byte {
	h.replayMu.Lock()
	defer h.replayMu.Unlock()
	rb := h.replay[chatID]
	if rb == nil {
		return nil
	}
	return rb.since(lastSeq)
}

// evictIdleReplay освобождает буферы чатов без событий дольше replayIdleTTL.
func (h *Hub) evictIdleReplay(now time.Time) {
	h.replayMu.Lock()
	defer h.replayMu.Unlock()
	for _, rb := range h.replay {
		if rb.buf != nil && now.Sub(rb.lastWrite) > replayIdleTTL {
			rb.release()
		}
	}
}

// CloseAll отключает все живые сокеты при graceful shutdown. Вызывать до
// отмены ctx, переданного в Run.
func (h *Hub) CloseAll() {
	seen := make(map[*Client]struct{})
	h.mu.RLock()
	for _, clients := range h.chatClients {
		for c := range clients {
			seen[c] = struct{}{}
		}
	}
	for c := range h.queueClients {
		seen[c] = struct{}{}
	}
	h.mu.RUnlock()
	for c := range seen {
		c.close("server_shutdown")
	}
}

// injectSeq добавляет поле seq в JSON-байты WSOutgoing
func injectSeq(data []byte, seq uint64) []byte {
	var out models.WSOutgoing
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	out.Seq = seq
	b, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return b
}
