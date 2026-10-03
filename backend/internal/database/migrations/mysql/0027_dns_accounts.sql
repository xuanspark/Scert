CREATE TABLE dns_accounts (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    owner_user_id VARCHAR(36) NOT NULL,
    provider VARCHAR(36) NOT NULL,
    credential_ref VARCHAR(255) NOT NULL,
    status VARCHAR(16) NOT NULL,
    verification_state VARCHAR(16) NOT NULL,
    revision INT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE KEY uq_dns_accounts_credential (credential_ref),
    INDEX idx_dns_accounts_project (project_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
ALTER TABLE dns_accounts ADD CONSTRAINT fk_dns_accounts_project FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE RESTRICT;
