CREATE TABLE IF NOT EXISTS projects (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    description TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO projects (id, name, description)
SELECT 'default', '默认项目', '历史资源的默认归属项目'
WHERE NOT EXISTS (SELECT 1 FROM projects WHERE id = 'default');
