CREATE TABLE recovery_events (
    disposition_id VARCHAR(64) NOT NULL,
    event_type VARCHAR(32) NOT NULL,
    result_code VARCHAR(64) NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (disposition_id, event_type)
);
