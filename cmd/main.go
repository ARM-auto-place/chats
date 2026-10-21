package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"

	"github.com/ARM-auto-place/chats/internal/config"
	chatService "github.com/ARM-auto-place/chats/internal/service"
	"github.com/ARM-auto-place/chats/internal/service/ws"
	postgres "github.com/ARM-auto-place/chats/internal/storage/postgres"
	customHandlers "github.com/ARM-auto-place/chats/internal/transport/custom-handlers"
	transportHttp "github.com/ARM-auto-place/chats/internal/transport/http"
	"github.com/ARM-auto-place/chats/internal/transport/jsonRPC/externalapi"
	accessTransport "github.com/mbatimel/AMC/access/pkg/client/transport"
)

const (
	serviceName     = "chat-api"
	cleanupInterval = 5 * time.Minute
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout}).With().Str("serviceName", serviceName).Logger()

	if err := config.LoadEnvFile(".env"); err != nil && !os.IsNotExist(err) {
		log.Fatal().Err(err).Msg("load env file")
	}
	cfg := config.LoadConfig()

	pool, err := postgres.NewPool(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to postgres")
	}
	defer pool.Close()

	postgresStorage := postgres.New(pool)
	accessClient := accessTransport.NewClientAccessAPI(cfg.AccessURL)

	ws.Configure(cfg.WS)
	hub := ws.NewHub(accessClient, log.Logger)
	svc := chatService.New(log.Logger, postgresStorage, accessClient, hub)

	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()
	go hub.Run(hubCtx)

	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	defer cleanupCancel()
	cleanupWorker := chatService.NewCleanupWorker(postgresStorage, log.Logger, cleanupInterval, hub)
	go cleanupWorker.Run(cleanupCtx)

	app := externalapi.New(log.Logger,
		externalapi.Use(customHandlers.ValidateChatRequest),
		externalapi.ChatAPI(externalapi.NewChatAPI(svc)),
	).WithLog().WithMetrics()
	ws.RegisterRoute(app.Fiber(), hub, svc, accessClient)

	server := &fasthttp.Server{Handler: app.Fiber().Handler()}
	healthServer := transportHttp.NewHealthServer()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		log.Info().Str("address", cfg.BindAddr).Msg("chat external api server started")
		if serveErr := server.ListenAndServe(cfg.BindAddr); serveErr != nil {
			log.Error().Err(serveErr).Msg("chat server stopped")
			shutdown <- syscall.SIGTERM
		}
	}()
	go func() {
		if healthErr := healthServer.Start(":9094"); healthErr != nil {
			log.Error().Err(healthErr).Msg("chat health server stopped")
		}
	}()

	<-shutdown
	cleanupCancel()
	// CloseAll до hubCancel: закрытие клиента шлёт unregister координатору хаба.
	hub.CloseAll()
	hubCancel()
	if err = healthServer.Stop(); err != nil {
		log.Error().Err(err).Msg("failed to stop health server")
	}
	if err = server.Shutdown(); err != nil {
		log.Error().Err(err).Msg("failed to shutdown chat server")
	}
}
