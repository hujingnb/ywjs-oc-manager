ALTER TABLE aicc_messages
    ADD COLUMN client_message_id CHAR(36) NULL AFTER hermes_message_id,
    ADD UNIQUE KEY uk_aicc_messages_session_direction_client (session_id, direction, client_message_id);
