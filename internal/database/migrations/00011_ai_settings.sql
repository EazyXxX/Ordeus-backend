-- +goose Up
CREATE TABLE ai_settings (
    user_id         uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    provider        text NOT NULL DEFAULT 'local' CHECK (provider IN ('local', 'remote')),
    local_model     text NOT NULL DEFAULT '',
    remote_base_url text NOT NULL DEFAULT '',
    remote_model    text NOT NULL DEFAULT '',
    -- Encrypted by the server. Only the last characters are kept in the clear.
    remote_api_key  bytea,
    remote_key_hint text NOT NULL DEFAULT '',
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER ai_settings_set_updated_at BEFORE UPDATE ON ai_settings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The model used to be one shared setting, so every account keeps that choice.
INSERT INTO ai_settings (user_id, local_model)
SELECT u.id, s.value FROM users u JOIN app_settings s ON s.key = 'ai.model';

DROP TABLE app_settings;

-- +goose Down
CREATE TABLE app_settings (
    key        text PRIMARY KEY,
    value      text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER app_settings_set_updated_at BEFORE UPDATE ON app_settings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

DROP TABLE ai_settings;
