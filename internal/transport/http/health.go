package http

import "github.com/gofiber/fiber/v2"

type HealthServer struct {
	server *fiber.App
}

func NewHealthServer() *HealthServer {
	health := &HealthServer{server: fiber.New(fiber.Config{DisableStartupMessage: true})}
	health.server.Get("/liveness", probesHandler)
	health.server.Get("/readiness", probesHandler)
	return health
}

func probesHandler(ctx *fiber.Ctx) error {
	return ctx.SendStatus(fiber.StatusOK)
}

func (h *HealthServer) Start(bindURL string) error {
	return h.server.Listen(bindURL)
}

func (h *HealthServer) Stop() error {
	return h.server.Shutdown()
}
