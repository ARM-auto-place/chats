	UPDATE chats
		SET dialog_started_at = COALESCE(dialog_started_at, $2)
		WHERE id = $1