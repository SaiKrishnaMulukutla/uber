CREATE TABLE IF NOT EXISTS drivers (
    id            UUID PRIMARY KEY,
    name          VARCHAR(200) NOT NULL,
    email         VARCHAR(200) UNIQUE NOT NULL,
    phone         VARCHAR(50)  UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    vehicle_type  VARCHAR(50)  DEFAULT 'sedan',
    license_plate VARCHAR(20),
    status        VARCHAR(20)  DEFAULT 'available',
    rating        DOUBLE PRECISION DEFAULT 5.0,
    created_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_drivers_email  ON drivers(email);
CREATE INDEX IF NOT EXISTS idx_drivers_status ON drivers(status);
