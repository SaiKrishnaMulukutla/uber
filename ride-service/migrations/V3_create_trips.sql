CREATE TABLE IF NOT EXISTS trips (
    id           UUID PRIMARY KEY,
    rider_id     UUID NOT NULL REFERENCES users(id),
    driver_id    UUID REFERENCES drivers(id),
    pickup_lat   DOUBLE PRECISION,
    pickup_lng   DOUBLE PRECISION,
    drop_lat     DOUBLE PRECISION,
    drop_lng     DOUBLE PRECISION,
    fare         DECIMAL(12,2),
    status       VARCHAR(30) NOT NULL DEFAULT 'REQUESTED',
    requested_at TIMESTAMPTZ,
    started_at   TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_trips_rider_id  ON trips(rider_id);
CREATE INDEX IF NOT EXISTS idx_trips_driver_id ON trips(driver_id);
CREATE INDEX IF NOT EXISTS idx_trips_status    ON trips(status);
