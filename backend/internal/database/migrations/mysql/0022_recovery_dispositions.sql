CREATE TABLE recovery_dispositions (
    id VARCHAR(64) NOT NULL,
    transaction_id VARCHAR(160) NOT NULL,
    server_id VARCHAR(160) NOT NULL,
    operator_id VARCHAR(160) NOT NULL,
    action VARCHAR(32) NOT NULL,
    request_key VARCHAR(64) NOT NULL,
    record_digest VARCHAR(64) NOT NULL,
    server_digest VARCHAR(64) NOT NULL,
    observation_digest VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    result_code VARCHAR(64) NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_recovery_request (request_key)
);
