//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v4/pgxpool"
)

func TestInsertMessage_ConcurrentDuplicateCreatesOneRow(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	storage := New(pool)
	ownerID := mustInsertTestUser(t, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM chats WHERE owner_user_id = $1`, ownerID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, ownerID)
	})
	chat, err := storage.GetOrCreateChatByOwner(ctx, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	clientMsgID := uuid.New()
	start := make(chan struct{})
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, created, insertErr := storage.InsertMessage(ctx, chat.ID, ownerID, clientMsgID, "retry", false, false)
			results <- created
			errs <- insertErr
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	createdCount := 0
	for created := range results {
		if created {
			createdCount++
		}
	}
	for insertErr := range errs {
		if insertErr != nil {
			t.Fatal(insertErr)
		}
	}
	var rowCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE chat_id=$1 AND sender_id=$2 AND client_msg_id=$3`, chat.ID, ownerID, clientMsgID).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if createdCount != 1 || rowCount != 1 {
		t.Fatalf("expected one created result and one row, got created=%d rows=%d", createdCount, rowCount)
	}
}

func TestInsertMessage_ConcurrentAdminTakeoverPreventsLaterBotSave(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	storage := New(pool)
	ownerID := mustInsertTestUser(t, pool)
	adminID := mustInsertTestUser(t, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM chats WHERE owner_user_id = $1`, ownerID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1, $2)`, ownerID, adminID)
	})
	chat, err := storage.GetOrCreateChatByOwner(ctx, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	botID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	start := make(chan struct{})
	adminDone := make(chan error, 1)
	botDone := make(chan error, 1)
	go func() {
		<-start
		_, _, insertErr := storage.InsertMessage(ctx, chat.ID, adminID, uuid.New(), "takeover", true, false)
		adminDone <- insertErr
	}()
	go func() {
		<-start
		_, _, insertErr := storage.InsertMessage(ctx, chat.ID, botID, uuid.New(), "in flight", false, true)
		botDone <- insertErr
	}()
	close(start)
	if err = <-adminDone; err != nil {
		t.Fatalf("admin takeover failed: %v", err)
	}
	if botErr := <-botDone; botErr != nil && !errors.Is(botErr, ErrBotSilenced) {
		t.Fatalf("unexpected concurrent bot result: %v", botErr)
	}
	_, _, err = storage.InsertMessage(ctx, chat.ID, botID, uuid.New(), "after takeover", false, true)
	if !errors.Is(err, ErrBotSilenced) {
		t.Fatalf("bot saved after completed takeover: %v", err)
	}
}

func TestInsertMessage_IdempotencyParticipantAndBotTakeover(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	storage := New(pool)
	ownerID := mustInsertTestUser(t, pool)
	adminID := mustInsertTestUser(t, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM chats WHERE owner_user_id = $1`, ownerID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1, $2)`, ownerID, adminID)
	})
	chat, err := storage.GetOrCreateChatByOwner(ctx, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	clientMsgID := uuid.New()
	first, created, err := storage.InsertMessage(ctx, chat.ID, adminID, clientMsgID, "support", true, false)
	if err != nil || !created {
		t.Fatalf("first insert: created=%v err=%v", created, err)
	}
	second, created, err := storage.InsertMessage(ctx, chat.ID, adminID, clientMsgID, "support", true, false)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("duplicate insert: first=%s second=%s created=%v err=%v", first.ID, second.ID, created, err)
	}
	var participants int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM chat_participants WHERE chat_id=$1 AND user_id=$2`, chat.ID, adminID).Scan(&participants); err != nil || participants != 1 {
		t.Fatalf("participant was not created exactly once: count=%d err=%v", participants, err)
	}
	reloaded, _, err := storage.GetChatByID(ctx, chat.ID)
	if err != nil || reloaded.BotActive {
		t.Fatalf("admin message must silence bot: chat=%+v err=%v", reloaded, err)
	}
	_, _, err = storage.InsertMessage(ctx, chat.ID, uuid.MustParse("00000000-0000-0000-0000-000000000001"), uuid.New(), "late bot", false, true)
	if !errors.Is(err, ErrBotSilenced) {
		t.Fatalf("expected ErrBotSilenced, got %v", err)
	}
}

func TestListMessages_CompositeCursorDoesNotSkipEqualTimestamps(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	storage := New(pool)
	ownerID := mustInsertTestUser(t, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM chats WHERE owner_user_id = $1`, ownerID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, ownerID)
	})
	chat, err := storage.GetOrCreateChatByOwner(ctx, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, _, insertErr := storage.InsertMessage(ctx, chat.ID, ownerID, uuid.New(), "same time", false, false)
		if insertErr != nil {
			t.Fatal(insertErr)
		}
	}
	fixed := time.Now().UTC().Truncate(time.Second)
	if _, err = pool.Exec(ctx, `UPDATE messages SET sent_at=$2 WHERE chat_id=$1`, chat.ID, fixed); err != nil {
		t.Fatal(err)
	}
	first, hasNext, err := storage.ListMessages(ctx, chat.ID, nil, 2)
	if err != nil || !hasNext || len(first) != 2 {
		t.Fatalf("first page: len=%d hasNext=%v err=%v", len(first), hasNext, err)
	}
	cursor := &MessageCursor{SentAt: first[1].SentAt, ID: first[1].ID, HasID: true}
	second, hasNext, err := storage.ListMessages(ctx, chat.ID, cursor, 2)
	if err != nil || hasNext || len(second) != 1 || second[0].ID == first[0].ID || second[0].ID == first[1].ID {
		t.Fatalf("second page: messages=%+v hasNext=%v err=%v", second, hasNext, err)
	}
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "host=localhost port=5432 dbname=AMC sslmode=disable user=mbatimel password=mbatimel"
	}
	pool, err := pgxpool.Connect(context.Background(), dsn)
	if err != nil {
		t.Skipf("postgres not reachable, skipping integration test: %v", err)
	}
	return pool
}

func mustInsertTestUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, name, surename) VALUES ($1, 'Test', 'User') RETURNING id`,
		uuid.NewString()+"@test.local",
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	return id
}

func TestGetOrCreateChatByOwner_Idempotent(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	storage := New(pool)

	ownerID := mustInsertTestUser(t, pool)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM chats WHERE owner_user_id = $1`, ownerID)
		pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, ownerID)
	})

	first, err := storage.GetOrCreateChatByOwner(ctx, ownerID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	second, err := storage.GetOrCreateChatByOwner(ctx, ownerID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("expected the same chat, got %s and %s", first.ID, second.ID)
	}
	if !second.BotActive {
		t.Fatalf("expected a freshly created chat to have bot_active = true")
	}
}

func TestGetChatByID_NotFound(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	storage := New(pool)

	_, found, err := storage.GetChatByID(ctx, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatalf("expected found = false for a random, non-existent chat ID")
	}
}

func TestSetBotActive_And_MarkOwnerRead(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	storage := New(pool)

	ownerID := mustInsertTestUser(t, pool)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM chats WHERE owner_user_id = $1`, ownerID)
		pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, ownerID)
	})

	chat, err := storage.GetOrCreateChatByOwner(ctx, ownerID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err = storage.SetBotActive(ctx, chat.ID, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err = storage.MarkOwnerRead(ctx, chat.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reloaded, found, err := storage.GetChatByID(ctx, chat.ID)
	if err != nil || !found {
		t.Fatalf("expected chat to be found, err=%v", err)
	}
	if reloaded.BotActive {
		t.Fatalf("expected bot_active = false")
	}
	if reloaded.OwnerLastReadAt == nil || time.Since(*reloaded.OwnerLastReadAt) > time.Minute {
		t.Fatalf("expected owner_last_read_at to be set to roughly now, got %v", reloaded.OwnerLastReadAt)
	}
}

func TestInsertMessage_StampsDialogStartedOnce(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	storage := New(pool)

	ownerID := mustInsertTestUser(t, pool)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM messages WHERE chat_id IN (SELECT id FROM chats WHERE owner_user_id = $1)`, ownerID)
		pool.Exec(ctx, `DELETE FROM chats WHERE owner_user_id = $1`, ownerID)
		pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, ownerID)
	})

	chat, err := storage.GetOrCreateChatByOwner(ctx, ownerID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chat.DialogStartedAt != nil {
		t.Fatalf("expected a fresh chat to have no dialog_started_at")
	}

	if _, _, err = storage.InsertMessage(ctx, chat.ID, ownerID, uuid.New(), "hello", false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	first, _, err := storage.GetChatByID(ctx, chat.ID)
	if err != nil || first.DialogStartedAt == nil {
		t.Fatalf("expected dialog_started_at to be set, err=%v", err)
	}

	if _, _, err = storage.InsertMessage(ctx, chat.ID, ownerID, uuid.New(), "again", false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, _, err := storage.GetChatByID(ctx, chat.ID)
	if err != nil || second.DialogStartedAt == nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !first.DialogStartedAt.Equal(*second.DialogStartedAt) {
		t.Fatalf("expected dialog_started_at to stay fixed at the first message's time")
	}
}

func TestCleanupExpiredDialogs(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	storage := New(pool)

	ownerID := mustInsertTestUser(t, pool)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM messages WHERE chat_id IN (SELECT id FROM chats WHERE owner_user_id = $1)`, ownerID)
		pool.Exec(ctx, `DELETE FROM chats WHERE owner_user_id = $1`, ownerID)
		pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, ownerID)
	})

	chat, err := storage.GetOrCreateChatByOwner(ctx, ownerID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, _, err = storage.InsertMessage(ctx, chat.ID, ownerID, uuid.New(), "old message", false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Force the dialog to look 25h old so cleanup picks it up.
	if _, err = pool.Exec(ctx, `UPDATE chats SET dialog_started_at = now() - INTERVAL '25 hours' WHERE id = $1`, chat.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err = storage.SetBotActive(ctx, chat.ID, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cleared, err := storage.CleanupExpiredDialogs(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cleared) < 1 {
		t.Fatalf("expected at least 1 chat cleared, got %d", len(cleared))
	}

	reloaded, _, err := storage.GetChatByID(ctx, chat.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reloaded.DialogStartedAt != nil {
		t.Fatalf("expected dialog_started_at to be reset to nil")
	}
	if !reloaded.BotActive {
		t.Fatalf("expected bot_active to be reset to true")
	}

	messages, _, err := storage.ListMessages(ctx, chat.ID, nil, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected messages to be wiped, got %d", len(messages))
	}
}
