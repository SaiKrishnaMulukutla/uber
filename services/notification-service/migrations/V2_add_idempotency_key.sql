ALTER TABLE notifications
    ADD COLUMN IF NOT EXISTS idempotency_key VARCHAR(255);

CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_idempotency
    ON notifications(idempotency_key)
    WHERE idempotency_key IS NOT NULL;
