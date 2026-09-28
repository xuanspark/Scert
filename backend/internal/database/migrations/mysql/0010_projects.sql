CREATE TABLE IF NOT EXISTS projects (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    description TEXT NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

INSERT INTO projects (id, name, description)
SELECT 'default', '默认项目', '历史资源的默认归属项目'
WHERE NOT EXISTS (SELECT 1 FROM projects WHERE id = 'default');
