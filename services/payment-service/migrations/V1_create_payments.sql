CREATE TABLE IF NOT EXISTS payments (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id              UUID        NOT NULL UNIQUE,
    rider_id             UUID        NOT NULL,
    driver_id            UUID        NOT NULL,
    amount               DECIMAL(12,2) NOT NULL,
    status               VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    payment_method       VARCHAR(30) NOT NULL DEFAULT 'cash',
    provider             VARCHAR(20) NOT NULL DEFAULT 'cash',
    provider_order_id    VARCHAR(100),
    provider_payment_id  VARCHAR(100),
    provider_signature   VARCHAR(512),
    failure_reason       TEXT,
    metadata             JSONB       NOT NULL DEFAULT '{}',
    attempts_count       INT         NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at         TIMESTAMPTZ,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_payments_rider_id          ON payments(rider_id);
CREATE INDEX IF NOT EXISTS idx_payments_driver_id         ON payments(driver_id);
CREATE INDEX IF NOT EXISTS idx_payments_provider_order_id ON payments(provider_order_id);
