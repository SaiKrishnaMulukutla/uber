CREATE TABLE IF NOT EXISTS notifications (
    id               UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID         NOT NULL,
    type             VARCHAR(50)  NOT NULL,
    title            VARCHAR(255) NOT NULL,
    body             TEXT         NOT NULL,
    read             BOOLEAN      DEFAULT FALSE,
    idempotency_key  VARCHAR(255),
    created_at       TIMESTAMPTZ  DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_notifications_user_created ON notifications(user_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_idempotency ON notifications(idempotency_key) WHERE idempotency_key IS NOT NULL;
