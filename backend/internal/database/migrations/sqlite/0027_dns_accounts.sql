CREATE TABLE dns_accounts (
    id TEXT NOT NULL PRIMARY KEY,
    name TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    owner_user_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    credential_ref TEXT NOT NULL,
    status TEXT NOT NULL,
    verification_state TEXT NOT NULL,
    revision INTEGER NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX uq_dns_accounts_credential ON dns_accounts (credential_ref);
CREATE INDEX idx_dns_accounts_project ON dns_accounts (project_id, id);
