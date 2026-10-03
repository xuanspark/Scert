CREATE TABLE recovery_dispositions (
    id TEXT NOT NULL PRIMARY KEY,
    transaction_id TEXT NOT NULL,
    server_id TEXT NOT NULL,
    operator_id TEXT NOT NULL,
    action TEXT NOT NULL,
    request_key TEXT NOT NULL UNIQUE,
    record_digest TEXT NOT NULL,
    server_digest TEXT NOT NULL,
    observation_digest TEXT NOT NULL,
    status TEXT NOT NULL,
    result_code TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
