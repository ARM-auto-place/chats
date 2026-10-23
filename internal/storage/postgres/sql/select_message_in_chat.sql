SELECT id, chat_id, sender_id, text, sent_at, is_system
		FROM messages
		WHERE chat_id = $1 AND sender_id = $2 AND client_msg_id = $3;