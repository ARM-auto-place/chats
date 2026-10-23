INSERT INTO chat_participants (chat_id, user_id)
			VALUES ($1, $2)
			ON CONFLICT (chat_id, user_id) DO NOTHING;