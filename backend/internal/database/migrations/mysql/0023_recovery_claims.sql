CREATE TABLE recovery_claims (
    transaction_id VARCHAR(160) NOT NULL,
    disposition_id VARCHAR(64) NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (transaction_id),
    UNIQUE KEY uq_recovery_claim (disposition_id)
);
