INSERT INTO chat_participants (chat_id, user_id, last_read_at)
		VALUES ($1, $2, now())
		ON CONFLICT (chat_id, user_id) DO UPDATE SET last_read_at = now();