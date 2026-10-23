package ws

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

func TestHub_RegisterOwner_SubscribesOnlyThatChat(t *testing.T) {
	hub := startHub(t, nil)
	chatA, chatB := uuid.New(), uuid.New()
	client := newTestClient(hub, &fakeConn{}, nil, false, chatA)

	register(t, hub, client, chatA)

	if subsB := hub.Subscribers(chatB); len(subsB) != 0 {
		t.Fatalf("expected no subscribers on chatB, got %v", subsB)
	}
}

func TestHub_RegisterQueue_SubscribesEveryChat(t *testing.T) {
	hub := startHub(t, nil)
	admin := newTestClient(hub, &fakeConn{}, nil, true, uuid.Nil)
	register(t, hub, admin, uuid.New())

	for _, chatID := range []uuid.UUID{uuid.New(), uuid.New()} {
		subs := hub.Subscribers(chatID)
		if len(subs) != 1 || subs[0] != admin {
			t.Fatalf("expected the queue client to see chat %s, got %v", chatID, subs)
		}
	}
}

func TestHub_Subscribers_CombinesOwnerAndQueue(t *testing.T) {
	hub := startHub(t, nil)
	chatID := uuid.New()
	register(t, hub, newTestClient(hub, &fakeConn{}, nil, false, chatID), chatID)
	register(t, hub, newTestClient(hub, &fakeConn{}, nil, true, uuid.Nil), chatID)

	if subs := hub.Subscribers(chatID); len(subs) != 2 {
		t.Fatalf("expected 2 subscribers (owner + queue), got %d", len(subs))
	}
}

func TestHub_Close_Unregisters(t *testing.T) {
	hub := startHub(t, nil)
	chatID := uuid.New()
	owner := newTestClient(hub, &fakeConn{}, nil, false, chatID)
	admin := newTestClient(hub, &fakeConn{}, nil, true, uuid.Nil)
	register(t, hub, owner, chatID)
	register(t, hub, admin, chatID)

	owner.close("test")
	admin.close("test")

	eventually(t, func() bool { return len(hub.Subscribers(chatID)) == 0 }, "expected no subscribers after close")
}

func TestHub_RegisterAfterClose_IsIgnored(t *testing.T) {
	hub := startHub(t, nil)
	chatID := uuid.New()
	client := newTestClient(hub, &fakeConn{}, nil, false, chatID)

	// unregister may be picked by the coordinator before Register.
	client.close("test")
	hub.Register <- client

	time.Sleep(50 * time.Millisecond)
	if subs := hub.Subscribers(chatID); len(subs) != 0 {
		t.Fatalf("closed client must not stay registered, got %v", subs)
	}
}

func TestHub_Broadcast_InjectsMonotonicSeqPerChat(t *testing.T) {
	hub := startHub(t, nil)
	chatA, chatB := uuid.New(), uuid.New()
	admin := newTestClient(hub, &fakeConn{}, nil, true, uuid.Nil)
	register(t, hub, admin, chatA)

	ctx := context.Background()
	for _, chatID := range []uuid.UUID{chatA, chatA, chatB} {
		if err := hub.Broadcast(ctx, chatID, stageFrame(t, chatID), "stage_changed"); err != nil {
			t.Fatal(err)
		}
	}

	got := map[uuid.UUID][]uint64{}
	for range 3 {
		out := recv(t, admin)
		got[out.ChatID] = append(got[out.ChatID], out.Seq)
	}
	if a := got[chatA]; len(a) != 2 || a[0] != 1 || a[1] != 2 {
		t.Fatalf("expected chatA seq [1 2], got %v", a)
	}
	if b := got[chatB]; len(b) != 1 || b[0] != 1 {
		t.Fatalf("expected chatB seq [1], got %v", b)
	}
}

func TestHub_ReplaySince_ReturnsOnlyNewer(t *testing.T) {
	hub := startHub(t, nil)
	chatID := uuid.New()
	admin := newTestClient(hub, &fakeConn{}, nil, true, uuid.Nil)
	register(t, hub, admin, chatID)

	for range 3 {
		_ = hub.Broadcast(context.Background(), chatID, stageFrame(t, chatID), "stage_changed")
	}
	for range 3 {
		recv(t, admin)
	}

	if frames := hub.ReplaySince(chatID, 1); len(frames) != 2 {
		t.Fatalf("expected 2 frames after seq 1, got %d", len(frames))
	}
	if frames := hub.ReplaySince(uuid.New(), 0); frames != nil {
		t.Fatalf("expected nil for unknown chat, got %d frames", len(frames))
	}
}

func TestRingBuffer_OverwritesOldest(t *testing.T) {
	rb := &RingBuffer{}
	now := time.Now()
	for range replaySize + 5 {
		rb.write([]byte("x"), now)
	}
	if frames := rb.since(0); len(frames) != replaySize {
		t.Fatalf("expected %d frames, got %d", replaySize, len(frames))
	}
	if frames := rb.since(replaySize); len(frames) != 5 {
		t.Fatalf("expected 5 frames newer than seq %d, got %d", replaySize, len(frames))
	}
}

func TestHub_EvictIdleReplay_KeepsSeq(t *testing.T) {
	hub := NewHub(nil, zerolog.Nop())
	chatID := uuid.New()
	old := time.Now().Add(-2 * replayIdleTTL)
	rb := &RingBuffer{}
	rb.write([]byte("x"), old)
	hub.replay[chatID] = rb

	hub.evictIdleReplay(time.Now())

	if frames := hub.ReplaySince(chatID, 0); len(frames) != 0 {
		t.Fatalf("expected evicted buffer to be empty, got %d", len(frames))
	}
	if next := rb.write([]byte("y"), time.Now()); next != 2 {
		t.Fatalf("seq must survive eviction, got %d", next)
	}
}

func TestHub_BroadcastDropsRevokedAdmin(t *testing.T) {
	chatID := uuid.New()
	conn := &fakeConn{}
	hub := startHub(t, &fakeAccess{allow: false})
	admin := newTestClient(hub, conn, nil, true, uuid.Nil)
	register(t, hub, admin, chatID)

	_ = hub.Broadcast(context.Background(), chatID, stageFrame(t, chatID), "stage_changed")

	eventually(t, func() bool { return conn.isClosed() && len(hub.Subscribers(chatID)) == 0 }, "revoked admin must be disconnected")
	if len(admin.send) != 0 {
		t.Fatal("revoked admin must not receive the frame")
	}
}

func TestHub_CloseAll_DisconnectsEveryone(t *testing.T) {
	hub := startHub(t, nil)
	chatID := uuid.New()
	ownerConn, adminConn := &fakeConn{}, &fakeConn{}
	register(t, hub, newTestClient(hub, ownerConn, nil, false, chatID), chatID)
	register(t, hub, newTestClient(hub, adminConn, nil, true, uuid.Nil), chatID)

	hub.CloseAll()

	if !ownerConn.isClosed() || !adminConn.isClosed() {
		t.Fatal("expected every connection to be closed")
	}
	eventually(t, func() bool { return len(hub.Subscribers(chatID)) == 0 }, "expected no subscribers after CloseAll")
}

func TestHub_BroadcastAfterStop_ReturnsError(t *testing.T) {
	hub := NewHub(nil, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	cancel()
	<-hub.done

	// fill the shard so the send can't succeed by buffering
	for range BroadcastBuf {
		hub.shardFor(uuid.Nil).broadcast <- chatEvent{}
	}
	if err := hub.Broadcast(context.Background(), uuid.Nil, []byte("{}"), "test"); err == nil {
		t.Fatal("expected error from a stopped hub")
	}
}
