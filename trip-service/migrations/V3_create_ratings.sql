CREATE TABLE IF NOT EXISTS ratings (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id    UUID NOT NULL,
    rater_id   UUID NOT NULL,
    rater_role VARCHAR(10) NOT NULL,
    ratee_id   UUID NOT NULL,
    ratee_role VARCHAR(10) NOT NULL,
    score      SMALLINT NOT NULL CHECK (score >= 1 AND score <= 5),
    comment    TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(trip_id, rater_id)
);

CREATE INDEX IF NOT EXISTS idx_ratings_trip_id ON ratings(trip_id);
CREATE INDEX IF NOT EXISTS idx_ratings_ratee_id ON ratings(ratee_id);
