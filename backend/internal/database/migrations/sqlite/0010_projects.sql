CREATE TABLE IF NOT EXISTS projects (
    id TEXT NOT NULL PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO projects (id, name, description)
SELECT 'default', '默认项目', '历史资源的默认归属项目'
WHERE NOT EXISTS (SELECT 1 FROM projects WHERE id = 'default');
