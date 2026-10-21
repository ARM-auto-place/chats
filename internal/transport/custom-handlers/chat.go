package custom_handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	externalapi "github.com/ARM-auto-place/chats/pkg/interfaces/externalapi"
)

const ServiceName = "chat"

func GetOrCreateSupportChat(ctx *fiber.Ctx, svc externalapi.ChatAPI, userID uuid.UUID) error {
	return handle(ctx, "post", "/v1/chat/support", "GetOrCreateSupportChat", map[string]interface{}{
		"userID": userID,
	}, func() (interface{}, error) {
		return svc.GetOrCreateSupportChat(ctx.UserContext(), userID)
	})
}

func ListSupportQueue(ctx *fiber.Ctx, svc externalapi.ChatAPI, userID uuid.UUID, limit int, offset int) error {
	return handle(ctx, "get", "/v1/chat/queue", "ListSupportQueue", map[string]interface{}{
		"userID": userID,
		"limit":  limit,
		"offset": offset,
	}, func() (interface{}, error) {
		return svc.ListSupportQueue(ctx.UserContext(), userID, limit, offset)
	})
}

func GetMessages(ctx *fiber.Ctx, svc externalapi.ChatAPI, userID uuid.UUID, chatID uuid.UUID, before string, limit int) error {
	return handle(ctx, "get", "/v1/chat/messages", "GetMessages", map[string]interface{}{
		"userID": userID,
		"chatID": chatID,
		"before": before,
		"limit":  limit,
	}, func() (interface{}, error) {
		return svc.GetMessages(ctx.UserContext(), userID, chatID, before, limit)
	})
}

func MarkRead(ctx *fiber.Ctx, svc externalapi.ChatAPI, userID uuid.UUID, chatID uuid.UUID) error {
	return handle(ctx, "post", "/v1/chat/read", "MarkRead", map[string]interface{}{
		"userID": userID,
		"chatID": chatID,
	}, func() (interface{}, error) {
		return svc.MarkRead(ctx.UserContext(), userID, chatID)
	})
}

func SetChatStage(ctx *fiber.Ctx, svc externalapi.ChatAPI, userID uuid.UUID, chatID uuid.UUID, stage string) error {
	return handle(ctx, "post", "/v1/chat/stage", "SetChatStage", map[string]interface{}{
		"userID": userID,
		"chatID": chatID,
		"stage":  stage,
	}, func() (interface{}, error) {
		return svc.SetChatStage(ctx.UserContext(), userID, chatID, stage)
	})
}

func handle(
	ctx *fiber.Ctx,
	method string,
	path string,
	methodName string,
	fields map[string]interface{},
	call func() (interface{}, error),
) error {
	var err error

	defer func(begin time.Time) {
		fields["method"] = method
		fields["path"] = path
		fields["methodName"] = methodName
		fields["serviceName"] = ServiceName
		fields["took"] = time.Since(begin).String()

		l := log.Info()
		if err != nil {
			l = log.Error().Err(err)
		}
		l.Fields(fields).Msg("call")
	}(time.Now())

	data, err := call()
	if err != nil {
		sendResponse(ctx, log.Logger, nil, err)
		return nil
	}
	sendResponse(ctx, log.Logger, data, nil)
	return nil
}
