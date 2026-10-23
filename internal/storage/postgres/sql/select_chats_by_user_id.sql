SELECT
	c.id,
    c.user_id,
    c.last_read_at,
    c.dialog_started_at,
    c.bot_active,
    c.created_at,
	lm.text,
    lm.sent_at,
	COALESCE(unread.cnt, 0) AS unread
FROM chats c
	LEFT JOIN LATERAL (
		SELECT text, sent_at FROM messages WHERE chat_id = c.id ORDER BY sent_at DESC, id DESC LIMIT 1
	) lm ON true
	LEFT JOIN LATERAL (
		SELECT count(*) AS cnt FROM messages m
		    WHERE m.chat_id = c.id AND m.sender_id = c.owner_user_id
			AND m.sent_at > COALESCE(
			    (SELECT last_read_at FROM chat_participants WHERE chat_id = c.id AND user_id = $1),
			      '-infinity'::timestamptz
			  )
		) unread ON true
	ORDER BY COALESCE(lm.sent_at, c.created_at) DESC, c.id DESC
	LIMIT $2 OFFSET $3;