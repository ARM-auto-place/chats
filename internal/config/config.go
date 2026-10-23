package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

type Config struct {
	PGHost     string
	PGPort     string
	PGDB       string
	PGUser     string
	PGPassword string
	BindAddr   string
	AccessURL  string
	// AssistantURL is assistant_bot's JSON-RPC endpoint (…/rpc). Empty
	// disables bot replies.
	AssistantURL     string
	AssistantTimeout time.Duration
	WS               WSConfig
}

// WSConfig holds the websocket client and hub tuning knobs — see
// internal/service/ws.Configure.
type WSConfig struct {
	WSClient WSClientConfig
	WSHub    WSHubConfig
}

type WSClientConfig struct {
	WriteWait            time.Duration
	PongWait             time.Duration
	PingPeriod           time.Duration // must be < PongWait
	MaxMessageSize       int64
	SendBufferSize       int
	ReadWorkerCount      int
	ReadWorkerBufferSize int
	ReadWorkerMaxRetries int
	ReadWorkerRetryDelay time.Duration
}

type WSHubConfig struct {
	RegisterBuf   int
	UnregisterBuf int
	BroadcastBuf  int
	ShardCount    int
}

func LoadConfig() Config {
	cfg := Config{
		PGHost:           GetEnv("PG_HOST", "localhost"),
		PGPort:           GetEnv("PG_PORT", "5432"),
		PGDB:             os.Getenv("PG_DB"),
		PGUser:           os.Getenv("PG_USER"),
		PGPassword:       os.Getenv("PG_PASSWORD"),
		BindAddr:         GetEnv("BIND_ADDR", ":8086"),
		AccessURL:        os.Getenv("ACCESS_URL"),
		AssistantURL:     os.Getenv("ASSISTANT_URL"),
		AssistantTimeout: GetEnvDuration("ASSISTANT_TIMEOUT", 30*time.Second),
		WS: WSConfig{
			WSClient: WSClientConfig{
				WriteWait:            GetEnvDuration("WS_WRITE_WAIT", 10*time.Second),
				PongWait:             GetEnvDuration("WS_PONG_WAIT", 60*time.Second),
				PingPeriod:           GetEnvDuration("WS_PING_PERIOD", 30*time.Second),
				MaxMessageSize:       int64(GetEnvInt("WS_MAX_MESSAGE_SIZE", 64*1024)),
				SendBufferSize:       GetEnvInt("WS_SEND_BUFFER_SIZE", 64),
				ReadWorkerCount:      GetEnvInt("WS_READ_WORKER_COUNT", 1),
				ReadWorkerBufferSize: GetEnvInt("WS_READ_WORKER_BUFFER_SIZE", 16),
				ReadWorkerMaxRetries: GetEnvInt("WS_READ_WORKER_MAX_RETRIES", 3),
				ReadWorkerRetryDelay: GetEnvDuration("WS_READ_WORKER_RETRY_DELAY", 100*time.Millisecond),
			},
			WSHub: WSHubConfig{
				RegisterBuf:   GetEnvInt("WS_HUB_REGISTER_BUF", 256),
				UnregisterBuf: GetEnvInt("WS_HUB_UNREGISTER_BUF", 256),
				BroadcastBuf:  GetEnvInt("WS_HUB_BROADCAST_BUF", 1024),
				ShardCount:    GetEnvInt("WS_HUB_SHARD_COUNT", 8),
			},
		},
	}
	if cfg.PGDB == "" || cfg.PGUser == "" || cfg.PGPassword == "" {
		log.Fatal().Msg("PG_DB, PG_USER and PG_PASSWORD must be specified")
	}
	if cfg.AccessURL == "" {
		log.Fatal().Msg("ACCESS_URL must be specified")
	}
	if cfg.WS.WSClient.PingPeriod >= cfg.WS.WSClient.PongWait {
		log.Fatal().Msg("WS_PING_PERIOD must be less than WS_PONG_WAIT")
	}
	return cfg
}

func GetEnv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func GetEnvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		log.Fatal().Err(err).Str("key", key).Msg("invalid int env value")
	}
	return parsed
}

func GetEnvDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		log.Fatal().Err(err).Str("key", key).Msg("invalid duration env value")
	}
	return parsed
}

func LoadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		value = os.Expand(value, os.Getenv)
		if err = os.Setenv(key, value); err != nil {
			return err
		}
	}
	return scanner.Err()
}
