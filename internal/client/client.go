package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/valyala/fasthttp"
)

// Assistant is implemented by internal/assistant.Client (assistant_bot's
// JSON-RPC "chat" method).
type Assistant interface {
	Chat(ctx context.Context, message string, sessionID string) (Answer, error)
}

// maxMessageRunes matches ChatRequest.message's max_length in
// assistant_bot/main.py — longer messages are rejected with -32602.
const maxMessageRunes = 2000

// AnswerType mirrors assistant_bot's AnswerType (result.type_id).
type AnswerType int

const (
	AnswerTypeAI            AnswerType = 1
	AnswerTypeFAQ           AnswerType = 2
	AnswerTypeHandoff       AnswerType = 3
	AnswerTypeClarification AnswerType = 4
)

type Answer struct {
	Text   string     `json:"answer"`
	TypeID AnswerType `json:"type_id"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("assistant rpc error %d: %s", e.Code, e.Message)
}

type rpcRequest struct {
	JSONRPC string     `json:"jsonrpc"`
	ID      string     `json:"id"`
	Method  string     `json:"method"`
	Params  chatParams `json:"params"`
}

type chatParams struct {
	Message   string `json:"message"`
	SessionID string `json:"session_id,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  *Answer   `json:"result"`
	Error   *RPCError `json:"error"`
}

// Client calls assistant_bot's JSON-RPC 2.0 endpoint (POST /rpc, method "chat").
type Client struct {
	url     string
	timeout time.Duration
	http    *fasthttp.Client
	nextID  atomic.Uint64
}

// NewClient takes the full endpoint URL, e.g. http://localhost:8000/rpc.
// timeout applies when ctx carries no deadline of its own.
func NewClient(url string, timeout time.Duration) *Client {
	return &Client{url: url, timeout: timeout, http: &fasthttp.Client{}}
}

// Chat asks the assistant a question. sessionID is passed through as
// params.session_id; the bot does not keep history yet.
func (c *Client) Chat(ctx context.Context, message string, sessionID string) (Answer, error) {
	if runes := []rune(message); len(runes) > maxMessageRunes {
		message = string(runes[:maxMessageRunes])
	}
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      strconv.FormatUint(c.nextID.Add(1), 10),
		Method:  "chat",
		Params:  chatParams{Message: message, SessionID: sessionID},
	})
	if err != nil {
		return Answer{}, err
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI(c.url)
	req.Header.SetMethod(fasthttp.MethodPost)
	req.Header.SetContentType("application/json")
	req.SetBody(body)

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(c.timeout)
	}
	if err = c.http.DoDeadline(req, resp, deadline); err != nil {
		return Answer{}, fmt.Errorf("assistant request: %w", err)
	}
	if resp.StatusCode() != fasthttp.StatusOK {
		return Answer{}, fmt.Errorf("assistant http status %d", resp.StatusCode())
	}

	var out rpcResponse
	if err = json.Unmarshal(resp.Body(), &out); err != nil {
		return Answer{}, fmt.Errorf("assistant response decode: %w", err)
	}
	// The bot answers HTTP 200 even on errors — see assistant_bot/README.md.
	if out.Error != nil {
		return Answer{}, out.Error
	}
	if out.Result == nil || out.Result.Text == "" {
		return Answer{}, errors.New("assistant returned empty answer")
	}
	return *out.Result, nil
}
