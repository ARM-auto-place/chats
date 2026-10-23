INSERT INTO messages (chat_id, sender_id, client_msg_id, text)
		VALUES ($1, $2, $3, $4)
		RETURNING id, chat_id, sender_id, text, sent_at, is_system