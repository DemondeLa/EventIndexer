CREATE TABLE IF NOT EXISTS sync_state (
    id            INT    PRIMARY KEY DEFAULT 1,
    last_block     BIGINT       NOT NULL DEFAULT 0,
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO sync_state (id, last_block) VALUES (1, 0)
ON CONFLICT (id) DO NOTHING;