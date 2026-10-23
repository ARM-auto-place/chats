INSERT INTO messages (chat_id, sender_id, client_msg_id, text, is_system)
		VALUES ($1, $2, $3, $4, true)
		RETURNING id, chat_id, sender_id, text, sent_at, is_system;