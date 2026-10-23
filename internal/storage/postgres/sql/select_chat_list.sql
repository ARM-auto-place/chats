			SELECT id, chat_id, sender_id, text, sent_at, is_system FROM messages
			WHERE chat_id = $1
			  AND (
			      $2::timestamptz IS NULL
			      OR sent_at < $2
			      OR ($3::uuid IS NOT NULL AND sent_at = $2 AND id < $3)
			  )
			ORDER BY sent_at DESC, id DESC
			LIMIT $4;