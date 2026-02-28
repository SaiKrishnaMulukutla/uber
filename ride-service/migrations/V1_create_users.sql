CREATE TABLE IF NOT EXISTS users (
    id            UUID PRIMARY KEY,
    name          VARCHAR(200) NOT NULL,
    email         VARCHAR(200) UNIQUE NOT NULL,
    phone         VARCHAR(50)  UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    rating        DOUBLE PRECISION DEFAULT 5.0,
    created_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_phone ON users(phone);
