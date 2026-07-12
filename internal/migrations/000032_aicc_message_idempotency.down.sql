ALTER TABLE aicc_messages
    DROP INDEX uk_aicc_messages_session_direction_client,
    DROP COLUMN client_message_id;
