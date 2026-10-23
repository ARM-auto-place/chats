package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func newRPCServer(t *testing.T, handle func(req rpcRequest) any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(handle(req))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestChat_ReturnsAnswer(t *testing.T) {
	var got rpcRequest
	srv := newRPCServer(t, func(req rpcRequest) any {
		got = req
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"answer": "Бесплатно", "type_id": 2}}
	})

	answer, err := NewClient(srv.URL, time.Second).Chat(context.Background(), "Сколько стоит?", "chat-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if answer.Text != "Бесплатно" || answer.TypeID != AnswerTypeFAQ {
		t.Fatalf("unexpected answer: %+v", answer)
	}
	if got.JSONRPC != "2.0" || got.Method != "chat" || got.ID == "" || got.Params.Message != "Сколько стоит?" || got.Params.SessionID != "chat-1" {
		t.Fatalf("unexpected request: %+v", got)
	}
}

func TestChat_RPCErrorWithHTTP200(t *testing.T) {
	srv := newRPCServer(t, func(req rpcRequest) any {
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32602, "message": "Invalid params"}}
	})

	_, err := NewClient(srv.URL, time.Second).Chat(context.Background(), "q", "")
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32602 {
		t.Fatalf("expected RPCError -32602, got %v", err)
	}
}

func TestChat_EmptyAnswer(t *testing.T) {
	srv := newRPCServer(t, func(req rpcRequest) any {
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"answer": "", "type_id": 1}}
	})

	if _, err := NewClient(srv.URL, time.Second).Chat(context.Background(), "q", ""); err == nil {
		t.Fatal("expected error for empty answer")
	}
}

func TestChat_TruncatesLongMessage(t *testing.T) {
	var got rpcRequest
	srv := newRPCServer(t, func(req rpcRequest) any {
		got = req
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"answer": "ok", "type_id": 1}}
	})

	if _, err := NewClient(srv.URL, time.Second).Chat(context.Background(), strings.Repeat("я", maxMessageRunes+10), ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := utf8.RuneCountInString(got.Params.Message); n != maxMessageRunes {
		t.Fatalf("expected %d runes, got %d", maxMessageRunes, n)
	}
}

func TestChat_HTTPStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	if _, err := NewClient(srv.URL, time.Second).Chat(context.Background(), "q", ""); err == nil {
		t.Fatal("expected error for non-200 status")
	}
}
