CREATE TABLE recovery_claims (
    transaction_id TEXT NOT NULL PRIMARY KEY,
    disposition_id TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL
);
