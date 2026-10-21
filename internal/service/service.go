package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	customErrors "github.com/ARM-auto-place/chats/internal/errors"
	"github.com/ARM-auto-place/chats/internal/storage/postgres"
)

const (
	defaultQueueLimit    = 20
	maxQueueLimit        = 100
	defaultMessagesLimit = 30
	maxMessagesLimit     = 100
)

// Storage is implemented by internal/storage/postgres.Storage.
type Storage interface {
	GetOrCreateChatByOwner(ctx context.Context, ownerUserID uuid.UUID) (postgres.ChatRow, error)
	GetChatByID(ctx context.Context, chatID uuid.UUID) (postgres.ChatRow, bool, error)
	ListQueue(ctx context.Context, viewerUserID uuid.UUID, limit int, offset int) ([]postgres.ChatListRow, int, error)
	SetBotActive(ctx context.Context, chatID uuid.UUID, active bool) error
	MarkOwnerRead(ctx context.Context, chatID uuid.UUID) error
	UpsertParticipantRead(ctx context.Context, chatID uuid.UUID, userID uuid.UUID) error
	InsertMessage(ctx context.Context, chatID uuid.UUID, senderID uuid.UUID, clientMsgID uuid.UUID, text string, isAdmin bool, isBot bool) (postgres.MessageRow, bool, error)
	InsertSystemMessage(ctx context.Context, chatID uuid.UUID, senderID uuid.UUID, text string) (postgres.MessageRow, error)
	ListMessages(ctx context.Context, chatID uuid.UUID, before *postgres.MessageCursor, limit int) ([]postgres.MessageRow, bool, error)
}

// AccessClient is implemented by back/access's generated HTTP client
// (github.com/mbatimel/AMC/access/pkg/client/transport.ClientAccessAPI).
type AccessClient interface {
	CheckAccess(ctx context.Context, userID uuid.UUID, role int) (allowed bool, err error)
}

// Broadcaster fans a ready-made WS frame out to every live subscriber of a
// chat. Implemented by internal/service/ws.Hub — declared here so this
// package never imports the ws package (which itself depends on ChatBackend).
type Broadcaster interface {
	Broadcast(ctx context.Context, chatID uuid.UUID, data []byte, event string) error
}

type service struct {
	logger       zerolog.Logger
	storage      Storage
	accessClient AccessClient
	hub          Broadcaster
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
func New(logger zerolog.Logger, storage Storage, accessClient AccessClient, hub Broadcaster) *service {
	return &service{logger: logger, storage: storage, accessClient: accessClient, hub: hub}
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
