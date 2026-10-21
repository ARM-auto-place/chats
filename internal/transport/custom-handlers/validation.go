package custom_handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	customErrors "github.com/ARM-auto-place/chats/internal/errors"
)

// ValidateChatRequest rejects malformed identity and request parameters before
// the generated transport can turn UUID parse failures into uuid.Nil.
func ValidateChatRequest(ctx *fiber.Ctx) error {
	path := ctx.Path()
	if path != "/api/v1/chat/support" && path != "/api/v1/chat/queue" &&
		path != "/api/v1/chat/messages" && path != "/api/v1/chat/read" && path != "/api/v1/chat/stage" {
		return ctx.Next()
	}

	userID, err := uuid.Parse(ctx.Get("X-User-Id"))
	if err != nil || userID == uuid.Nil {
		sendResponse(ctx, log.Logger, nil, customErrors.UnauthorizedError())
		return nil
	}

	if path == "/api/v1/chat/messages" || path == "/api/v1/chat/read" || path == "/api/v1/chat/stage" {
		chatID, parseErr := uuid.Parse(ctx.Query("chatID"))
		if parseErr != nil || chatID == uuid.Nil {
			sendResponse(ctx, log.Logger, nil, customErrors.BadRequestError().AddCause("field", "chatID"))
			return nil
		}
	}

	numericParams := []string(nil)
	if path == "/api/v1/chat/queue" {
		numericParams = []string{"limit", "offset"}
	} else if path == "/api/v1/chat/messages" {
		numericParams = []string{"limit"}
	}
	for _, name := range numericParams {
		raw := ctx.Query(name)
		if raw == "" {
			continue
		}
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || (name == "offset" && value < 0) {
			sendResponse(ctx, log.Logger, nil, customErrors.BadRequestError().AddCause("field", name))
			return nil
		}
	}

	return ctx.Next()
}
