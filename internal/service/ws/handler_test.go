package ws

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

func TestWebsocketRouteRejectsMalformedIdentity(t *testing.T) {
	app := fiber.New()
	RegisterRoute(app, NewHub(nil, zerolog.Nop()), nil, nil)
	req := httptest.NewRequest("GET", "/api/v1/chat/ws?userID=bad", nil)
	req.Header.Set("Connection", "upgrade")
	req.Header.Set("Upgrade", "websocket")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}
