		SELECT id FROM chats
		WHERE dialog_started_at IS NOT NULL
		  AND dialog_started_at < now() - INTERVAL '24 hours'
		FOR UPDATE;