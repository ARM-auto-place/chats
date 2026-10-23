INSERT INTO chats (user_id)
VALUES ($1)
ON CONFLICT (user_id) DO UPDATE SET
    last_read_at = NOW() 
RETURNING id, user_id, last_read_at, dialog_started_at, bot_active, created_at;