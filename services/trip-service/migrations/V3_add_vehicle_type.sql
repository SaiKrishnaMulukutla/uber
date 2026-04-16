ALTER TABLE trips
  ADD COLUMN IF NOT EXISTS vehicle_type VARCHAR(10) NOT NULL DEFAULT 'x';

CREATE INDEX IF NOT EXISTS idx_trips_vehicle_type ON trips(vehicle_type);
