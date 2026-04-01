CREATE TABLE IF NOT EXISTS payments (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id        UUID NOT NULL UNIQUE,
    rider_id       UUID NOT NULL,
    driver_id      UUID NOT NULL,
    amount         DECIMAL(12,2) NOT NULL,
    status         VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    payment_method VARCHAR(30) DEFAULT 'cash',
    created_at     TIMESTAMPTZ DEFAULT NOW(),
    completed_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_payments_rider_id ON payments(rider_id);
CREATE INDEX IF NOT EXISTS idx_payments_driver_id ON payments(driver_id);
