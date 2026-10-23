package postgres

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/ARM-auto-place/chats/internal/config"
	"github.com/ARM-auto-place/chats/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v4/pgxpool"
)

type Storage interface {
	GetOrCreateChatByOwner(ctx context.Context, ownerUserID uuid.UUID) (models.ChatRow, error)
	GetChatByID(ctx context.Context, chatID uuid.UUID) (models.ChatRow, bool, error)
	ListQueue(ctx context.Context, viewerUserID uuid.UUID, limit int, offset int) ([]models.ChatListRow, int, error)
	SetBotActive(ctx context.Context, chatID uuid.UUID, active bool) error
	MarkUserRead(ctx context.Context, chatID uuid.UUID) error
	UpsertParticipantRead(ctx context.Context, chatID uuid.UUID, userID uuid.UUID) error
	InsertMessage(ctx context.Context, chatID uuid.UUID, senderID uuid.UUID, clientMsgID uuid.UUID, text string, isAdmin bool, isBot bool) (models.MessageRow, bool, error)
	InsertSystemMessage(ctx context.Context, chatID uuid.UUID, senderID uuid.UUID, text string) (models.MessageRow, error)
	ListMessages(ctx context.Context, chatID uuid.UUID, before *models.MessageCursor, limit int) ([]models.MessageRow, bool, error)
}
type storage struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) Storage {
	return &storage{pool: pool}
}

func NewPool(cfg config.Config) (*pgxpool.Pool, error) {
	port, err := strconv.Atoi(cfg.PGPort)
	if err != nil {
		return nil, fmt.Errorf("parse PG_PORT: %w", err)
	}

	dsn := fmt.Sprintf(
		"host=%s port=%d dbname=%s sslmode=disable user=%s password=%s",
		cfg.PGHost, port, cfg.PGDB, cfg.PGUser, cfg.PGPassword,
	)

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.ConnectConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return pool, nil
}
