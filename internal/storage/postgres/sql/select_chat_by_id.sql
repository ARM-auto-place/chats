SELECT 
    id,
    user_id,
    last_read_at,
    dialog_started_at,
    bot_active,
    created_at
FROM chats 
WHERE id=$1;