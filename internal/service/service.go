package service

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/ARM-auto-place/chats/internal/client"
	customErrors "github.com/ARM-auto-place/chats/internal/errors"
	"github.com/ARM-auto-place/chats/internal/storage/postgres"
)

const (
	defaultQueueLimit    = 20
	maxQueueLimit        = 100
	defaultMessagesLimit = 30
	maxMessagesLimit     = 100
)

// Broadcaster fans a ready-made WS frame out to every live subscriber of a
// chat. Implemented by internal/service/ws.Hub — declared here so this
// package never imports the ws package (which itself depends on ChatBackend).
type Broadcaster interface {
	Broadcast(ctx context.Context, chatID uuid.UUID, data []byte, event string) error
}

type service struct {
	logger       zerolog.Logger
	storage      postgres.Storage
	accessClient client.AccessClient
	hub          Broadcaster

	assistant        client.Assistant
	assistantTimeout time.Duration
	botReplies       sync.WaitGroup
}

// New returns the concrete *service type (not the externalapi.ChatAPI
// interface, unlike back/orders' NewOrdersApiService) because callers need
// the same value to satisfy two different interfaces: externalapi.ChatAPI
// for the REST layer (cmd/main.go) and ChatBackend for the WS layer
// (internal/service/ws). A concrete type satisfies both automatically; an interface
// return would only expose the one it's declared as.
//
// hub may be nil (tests that don't care about WS broadcasts pass nil) — any
// code that broadcasts through it must guard the call.
func New(logger zerolog.Logger, storage postgres.Storage, accessClient client.AccessClient, hub Broadcaster) *service {
	return &service{logger: logger, storage: storage, accessClient: accessClient, hub: hub}
}

// WithAssistant enables bot replies: every new owner message in a chat with
// bot_active = true is forwarded to the assistant and its answer is posted
// back as BotUserID. Without it the bot stays silent.
func (s *service) WithAssistant(a client.Assistant, timeout time.Duration) *service {
	s.assistant = a
	s.assistantTimeout = timeout
	return s
}

func (s *service) isAdmin(ctx context.Context, userID uuid.UUID) (bool, error) {
	allowed, err := s.accessClient.CheckAccess(ctx, userID, RoleCodeAdmin)
	if err != nil {
		s.logger.Error().Err(err).Str("userID", userID.String()).Str("event", "admin_access_check").Msg("chat access check failed")
		return false, customErrors.InternalServerError().SetOuterError(err)
	}
	return allowed, nil
}

func (s *service) checkAdminAccess(ctx context.Context, userID uuid.UUID) error {
	allowed, err := s.isAdmin(ctx, userID)
	if err != nil {
		return err
	}
	if !allowed {
		return customErrors.ForbiddenError()
	}
	return nil
}
