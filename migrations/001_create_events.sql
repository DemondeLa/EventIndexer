CREATE TABLE IF NOT EXISTS events (
                                      id            BIGSERIAL    PRIMARY KEY,
                                      project_id    BIGINT       NOT NULL,
                                      name          TEXT         NOT NULL,
                                      url           TEXT         NOT NULL,
                                      submitter     TEXT         NOT NULL,
                                      tx_hash       TEXT         NOT NULL UNIQUE,
                                      block_number  BIGINT       NOT NULL,
                                      indexed_at    TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP
);