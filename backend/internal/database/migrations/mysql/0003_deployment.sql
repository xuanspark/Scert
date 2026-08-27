CREATE TABLE IF NOT EXISTS servers (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    address VARCHAR(253) NOT NULL,
    port INT NOT NULL,
    username VARCHAR(128) NOT NULL,
    private_key_ref VARCHAR(255) NOT NULL,
    host_key_fingerprint VARCHAR(255) NOT NULL,
    certificate_path VARCHAR(1024) NOT NULL,
    private_key_path VARCHAR(1024) NOT NULL,
    certificate_chain_path VARCHAR(1024) NOT NULL,
    web_server VARCHAR(32) NOT NULL,
    reload_command_json TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_servers_name (name)
);

CREATE TABLE IF NOT EXISTS domain_servers (
    domain_id VARCHAR(36) NOT NULL,
    server_id VARCHAR(36) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (domain_id, server_id),
    INDEX idx_domain_servers_server_id (server_id)
);
