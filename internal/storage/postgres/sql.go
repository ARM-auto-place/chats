package postgres

import (
	_ "embed"
)

//go:embed sql/insert_chat_by_user.sql
var sqlInsertChatByUser string

//go:embed sql/select_chat_by_id.sql
var sqlSelectChatByID string

//go:embed sql/select_chats_by_user_id.sql
var sqlSelectChatsByUserID string

//go:embed sql/select_chats_count.sql
var sqlSelectChatsCount string

//go:embed sql/upset_bot_status.sql
var sqlUpsetBotStatus string

//go:embed sql/upset_user_read.sql
var sqlUpsetUserRead string

//go:embed sql/upset_participant_read.sql
var SqlUpsettParticipantRead string

//go:embed sql/select_chat_by_dialog_started.sql
var SqlSelectChatByDialogStarteg string

//go:embed sql/delete_chats_by_ids.sql
var SqlDeleteChatsByIds string

//go:embed sql/update_chats_dialog_by_ids.sql
var SqlUpdateChatsDialogByIDs string

//go:embed sql/select_bot_status.sql
var SqlSelectBotStatus string

//go:embed sql/select_message_in_chat.sql
var SqlSelectMessageInChat string

//go:embed sql/insert_chat_participants.sql
var SqlInsertChatParticipants string

//go:embed sql/insert_messages.sql
var SqlInsertMessages string

//go:embed sql/insert_messages_system.sql
var SqlInsertMessagesSystem string

//go:embed sql/update_dialog_started.sql
var SqlUpdateDialogStarted string

//go:embed sql/select_chat_list.sql
var SqlSelectChatList string
