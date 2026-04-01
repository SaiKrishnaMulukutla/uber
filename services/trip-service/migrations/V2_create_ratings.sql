CREATE TABLE IF NOT EXISTS ratings (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id    UUID        NOT NULL REFERENCES trips(id),
    rater_id   UUID        NOT NULL,
    rater_role VARCHAR(10) NOT NULL CHECK (rater_role IN ('rider', 'driver')),
    ratee_id   UUID        NOT NULL,
    ratee_role VARCHAR(10) NOT NULL CHECK (ratee_role IN ('rider', 'driver')),
    score      SMALLINT    NOT NULL CHECK (score >= 1 AND score <= 5),
    comment    TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(trip_id, rater_id)
);
-- UNIQUE(trip_id, rater_id) already covers trip_id lookups.
-- Separate index for fetching all ratings received by a user/driver.
CREATE INDEX IF NOT EXISTS idx_ratings_ratee_id ON ratings(ratee_id);
