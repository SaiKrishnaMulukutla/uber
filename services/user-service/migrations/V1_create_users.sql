CREATE TABLE IF NOT EXISTS users (
    id            UUID             PRIMARY KEY,
    name          VARCHAR(200)     NOT NULL,
    email         VARCHAR(200)     NOT NULL UNIQUE,
    phone         VARCHAR(50)      NOT NULL UNIQUE,
    password_hash VARCHAR(255)     NOT NULL,
    rating        DOUBLE PRECISION NOT NULL DEFAULT 5.0,
    rating_count  INTEGER          NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);
