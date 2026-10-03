CREATE TABLE recovery_events (
    disposition_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    result_code TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (disposition_id, event_type)
);
