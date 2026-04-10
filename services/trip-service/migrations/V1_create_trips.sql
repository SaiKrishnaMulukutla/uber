CREATE TABLE IF NOT EXISTS trips (
    id               UUID             PRIMARY KEY,
    rider_id         UUID             NOT NULL,
    rider_email      TEXT             NOT NULL DEFAULT '',
    rider_phone      VARCHAR(20)      NOT NULL DEFAULT '',
    driver_id        UUID,
    pickup_lat       DOUBLE PRECISION NOT NULL,
    pickup_lng       DOUBLE PRECISION NOT NULL,
    drop_lat         DOUBLE PRECISION NOT NULL,
    drop_lng         DOUBLE PRECISION NOT NULL,
    fare             DECIMAL(12,2),
    status           VARCHAR(30)      NOT NULL DEFAULT 'REQUESTED',
    payment_method   VARCHAR(20)      NOT NULL DEFAULT 'cash',
    duration_seconds INTEGER,
    requested_at     TIMESTAMPTZ,
    started_at       TIMESTAMPTZ,
    completed_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_trips_rider_created  ON trips(rider_id,  created_at DESC);
CREATE INDEX IF NOT EXISTS idx_trips_driver_created ON trips(driver_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_trips_status         ON trips(status);
