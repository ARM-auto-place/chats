// Package externalapi describes the public chat API contract.
// @tg version=0.0.1
// @tg backend=chat
// @tg title=`chat`
// @tg servers=
//
//go:generate tg transport --services . --out ../../../internal/transport/jsonRPC/externalapi --outSwagger ../../../swaggers/externalapi/swagger.yaml
//go:generate go run ../../../internal/genswagger ../../../swaggers/externalapi/swagger.yaml
//go:generate tg client --services . --outPath ../../client/transport -go
package externalapi

import (
	"context"

	"github.com/google/uuid"

	"github.com/ARM-auto-place/chats/pkg/models"
)

// ChatAPI
// @tg http-server metrics log
// @tg http-prefix=/api
// @tg 200=github.com/ARM-auto-place/chats/swaggers/externalapi/models:Resp200
// @tg 400=github.com/ARM-auto-place/chats/swaggers/externalapi/models:Err400
// @tg 401=github.com/ARM-auto-place/chats/swaggers/externalapi/models:Err401
// @tg 403=github.com/ARM-auto-place/chats/swaggers/externalapi/models:Err403
// @tg 500=github.com/ARM-auto-place/chats/swaggers/externalapi/models:Err500
type ChatAPI interface {
	// GetOrCreateSupportChat ...
	// @tg http-method=POST
	// @tg http-path=/v1/chat/support
	// @tg http-cookies=userID|x-user-id
	// @tg http-response=github.com/ARM-auto-place/chats/internal/transport/custom-handlers:GetOrCreateSupportChat
	// @tg summary=`Получение своего чата с поддержкой`
	// @tg desc=`Возвращает единственный support-чат пользователя, создавая его при первом обращении`
	// @tg uuidPackage=github.com/google/uuid
	GetOrCreateSupportChat(ctx context.Context, userID uuid.UUID) (data models.Chat, err error)

	// ListSupportQueue ...
	// @tg http-method=GET
	// @tg http-path=/v1/chat/queue
	// @tg http-cookies=userID|x-user-id
	// @tg http-args=limit|limit
	// @tg http-args=offset|offset
	// @tg http-response=github.com/ARM-auto-place/chats/internal/transport/custom-handlers:ListSupportQueue
	// @tg summary=`Очередь support-чатов`
	// @tg desc=`Список всех чатов; доступен только сотрудникам с ролью Admin`
	// @tg uuidPackage=github.com/google/uuid
	ListSupportQueue(ctx context.Context, userID uuid.UUID, limit int, offset int) (data models.ChatListResponse, err error)

	// GetMessages ...
	// @tg http-method=GET
	// @tg http-path=/v1/chat/messages
	// @tg http-cookies=userID|x-user-id
	// @tg http-args=chatID|chatID
	// @tg http-args=before|before
	// @tg http-args=limit|limit
	// @tg http-response=github.com/ARM-auto-place/chats/internal/transport/custom-handlers:GetMessages
	// @tg summary=`История сообщений чата`
	// @tg desc=`Пагинация назад: before — sent_at,id последнего сообщения; legacy RFC3339 также принимается`
	// @tg uuidPackage=github.com/google/uuid
	// @tg chatID.format=uuid
	GetMessages(ctx context.Context, userID uuid.UUID, chatID uuid.UUID, before string, limit int) (data models.MessagesResponse, err error)

	// MarkRead ...
	// @tg http-method=POST
	// @tg http-path=/v1/chat/read
	// @tg http-cookies=userID|x-user-id
	// @tg http-args=chatID|chatID
	// @tg http-response=github.com/ARM-auto-place/chats/internal/transport/custom-handlers:MarkRead
	// @tg summary=`Отметить чат прочитанным`
	// @tg desc=`Обновляет read-receipt вызывающего пользователя для чата`
	// @tg uuidPackage=github.com/google/uuid
	// @tg chatID.format=uuid
	MarkRead(ctx context.Context, userID uuid.UUID, chatID uuid.UUID) (data bool, err error)

	// SetChatStage ...
	// @tg http-method=POST
	// @tg http-path=/v1/chat/stage
	// @tg http-cookies=userID|x-user-id
	// @tg http-args=chatID|chatID
	// @tg http-args=stage|stage
	// @tg http-response=github.com/ARM-auto-place/chats/internal/transport/custom-handlers:SetChatStage
	// @tg summary=`Переключение стадии диалога бот/оператор`
	// @tg desc=`stage: "operator" — сразу глушит бота (оператор подключается к диалогу), "bot" — возвращает диалог боту. Доступ: владелец чата или роль Admin`
	// @tg uuidPackage=github.com/google/uuid
	// @tg chatID.format=uuid
	SetChatStage(ctx context.Context, userID uuid.UUID, chatID uuid.UUID, stage string) (data models.Chat, err error)
}
