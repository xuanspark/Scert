CREATE TABLE IF NOT EXISTS servers (
    id TEXT NOT NULL PRIMARY KEY,
    name TEXT NOT NULL,
    address TEXT NOT NULL,
    port INTEGER NOT NULL,
    username TEXT NOT NULL,
    private_key_ref TEXT NOT NULL,
    host_key_fingerprint TEXT NOT NULL,
    certificate_path TEXT NOT NULL,
    private_key_path TEXT NOT NULL,
    certificate_chain_path TEXT NOT NULL,
    web_server TEXT NOT NULL,
    reload_command_json TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_servers_name ON servers(name);

CREATE TABLE IF NOT EXISTS domain_servers (
    domain_id TEXT NOT NULL,
    server_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (domain_id, server_id)
);

CREATE INDEX IF NOT EXISTS idx_domain_servers_server_id ON domain_servers(server_id);
