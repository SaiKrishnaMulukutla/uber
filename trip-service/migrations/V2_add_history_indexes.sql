CREATE INDEX IF NOT EXISTS idx_trips_rider_created
    ON trips(rider_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_trips_driver_created
    ON trips(driver_id, created_at DESC);
