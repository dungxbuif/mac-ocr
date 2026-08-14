package postgres

import (
	"context"
	"fmt"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    email         TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL DEFAULT '',
    role          TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
    disabled      BOOLEAN NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash TEXT;
UPDATE users SET password_hash = '' WHERE password_hash IS NULL;
ALTER TABLE users ALTER COLUMN password_hash SET DEFAULT '';
ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL;
ALTER TABLE users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'user';
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE TABLE IF NOT EXISTS account_configs (
    user_id        BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    rate_limit_rpm INT NOT NULL DEFAULT 60,
    doc_quota      BIGINT NOT NULL DEFAULT 0,
    doc_used       BIGINT NOT NULL DEFAULT 0,
    storage_quota_bytes BIGINT NOT NULL DEFAULT 0,
    storage_used_bytes BIGINT NOT NULL DEFAULT 0,
    storage_reserved_bytes BIGINT NOT NULL DEFAULT 0,
    quota_reset_at TIMESTAMPTZ,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by     BIGINT REFERENCES users(id)
);

ALTER TABLE account_configs ADD COLUMN IF NOT EXISTS storage_quota_bytes BIGINT NOT NULL DEFAULT 0;
ALTER TABLE account_configs ADD COLUMN IF NOT EXISTS storage_used_bytes BIGINT NOT NULL DEFAULT 0;
ALTER TABLE account_configs ADD COLUMN IF NOT EXISTS storage_reserved_bytes BIGINT NOT NULL DEFAULT 0;
ALTER TABLE account_configs DROP CONSTRAINT IF EXISTS account_configs_storage_nonnegative;
ALTER TABLE account_configs ADD CONSTRAINT account_configs_storage_nonnegative CHECK (
    storage_quota_bytes >= 0 AND storage_used_bytes >= 0 AND storage_reserved_bytes >= 0
);

INSERT INTO account_configs (user_id, rate_limit_rpm, doc_quota, doc_used)
SELECT id, 60, 0, 0 FROM users
ON CONFLICT (user_id) DO NOTHING;

CREATE TABLE IF NOT EXISTS api_keys (
    id             BIGSERIAL PRIMARY KEY,
    user_id        BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name           TEXT NOT NULL DEFAULT 'default',
    key_prefix     TEXT NOT NULL,
    key_hash       TEXT NOT NULL UNIQUE,
    rate_limit_rpm INT NOT NULL DEFAULT 60,
    revoked_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS rate_limit_rpm INT NOT NULL DEFAULT 60;

CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys(user_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash);

-- Batch is an ingestion convenience only. Remove legacy local-development
-- persistence from earlier schemas before creating/normalizing documents.
ALTER TABLE IF EXISTS documents DROP COLUMN IF EXISTS batch_id;
DROP TABLE IF EXISTS batches;

CREATE TABLE IF NOT EXISTS documents (
    id                 TEXT PRIMARY KEY,
    user_id            BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status             TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','processing','completed','failed','cancelled')),
    input_key          TEXT,
	input_sha256       TEXT NOT NULL DEFAULT '',
    input_content_type TEXT,
    input_size_bytes   BIGINT NOT NULL DEFAULT 0,
    options_json       JSONB,
    result_key         TEXT,
    result_text        TEXT,
    error_detail       TEXT,
    attempt_id         TEXT,
    attempt_count      INT NOT NULL DEFAULT 0,
    processing_until   TIMESTAMPTZ,
    terminal_event_id  TEXT,
    notification_type  TEXT CHECK (notification_type IN ('webhook','sse')),
    notification_url   TEXT,
    notification_secret BYTEA,
    result_expires_at  TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE documents ALTER COLUMN input_key DROP NOT NULL;
ALTER TABLE documents ADD COLUMN IF NOT EXISTS input_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE documents ADD COLUMN IF NOT EXISTS attempt_count INT NOT NULL DEFAULT 0;
ALTER TABLE documents ADD COLUMN IF NOT EXISTS processing_until TIMESTAMPTZ;
ALTER TABLE documents ADD COLUMN IF NOT EXISTS terminal_event_id TEXT;

CREATE INDEX IF NOT EXISTS idx_documents_queue ON documents(created_at) WHERE status = 'queued';
CREATE INDEX IF NOT EXISTS idx_documents_user ON documents(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_documents_result_expiry ON documents(result_expires_at) WHERE result_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_documents_processing_lease ON documents(processing_until) WHERE status = 'processing';
CREATE UNIQUE INDEX IF NOT EXISTS idx_documents_terminal_event ON documents(terminal_event_id) WHERE terminal_event_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS upload_reservations (
    object_key  TEXT PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    size_bytes  BIGINT NOT NULL CHECK (size_bytes > 0),
    state       TEXT NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved','consumed')),
    document_id TEXT REFERENCES documents(id) ON DELETE SET NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_upload_reservations_expiry
ON upload_reservations(expires_at)
WHERE state = 'reserved';
CREATE UNIQUE INDEX IF NOT EXISTS idx_upload_reservations_document
ON upload_reservations(document_id)
WHERE document_id IS NOT NULL;

-- Reconcile aggregate counters on every startup so legacy retained inputs are
-- charged and an interrupted cleanup cannot leave counters drifting.
UPDATE account_configs AS cfg SET
    storage_used_bytes = COALESCE((
        SELECT SUM(d.input_size_bytes) FROM documents d
        WHERE d.user_id = cfg.user_id AND d.input_key IS NOT NULL
    ), 0),
    storage_reserved_bytes = COALESCE((
        SELECT SUM(r.size_bytes) FROM upload_reservations r
        WHERE r.user_id = cfg.user_id AND r.state = 'reserved'
    ), 0);

CREATE TABLE IF NOT EXISTS notification_events (
    id                    TEXT PRIMARY KEY,
    user_id               BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    document_id           TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    event_type            TEXT NOT NULL,
    channel               TEXT NOT NULL CHECK (channel IN ('webhook','sse')),
    webhook_url           TEXT,
    webhook_secret        BYTEA,
    payload_json          JSONB NOT NULL,
    delivery_status       TEXT NOT NULL DEFAULT 'pending' CHECK (delivery_status IN ('pending','delivering','delivered')),
    attempt_count         INT NOT NULL DEFAULT 0,
    next_attempt_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_until          TIMESTAMPTZ,
    delivered_at          TIMESTAMPTZ,
    last_error            TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_notification_webhook_pending
ON notification_events(next_attempt_at, created_at)
WHERE channel = 'webhook' AND delivery_status IN ('pending','delivering');
CREATE INDEX IF NOT EXISTS idx_notification_sse_user
ON notification_events(user_id, id)
WHERE channel = 'sse';
CREATE UNIQUE INDEX IF NOT EXISTS idx_notification_document_terminal
ON notification_events(document_id, event_type);
CREATE OR REPLACE FUNCTION create_default_account_config()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO account_configs (user_id, rate_limit_rpm, doc_quota, doc_used)
    VALUES (NEW.id, 60, 0, 0)
    ON CONFLICT (user_id) DO NOTHING;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_user_config ON users;
CREATE TRIGGER trg_user_config
AFTER INSERT ON users
FOR EACH ROW EXECUTE FUNCTION create_default_account_config();
`

func (r *Repository) Migrate(ctx context.Context) error {
	if _, err := r.pool.Exec(ctx, schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}
