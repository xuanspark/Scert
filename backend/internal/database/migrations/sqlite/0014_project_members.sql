CREATE TABLE project_members (
    project_id TEXT NOT NULL REFERENCES projects(id),
    user_id TEXT NOT NULL REFERENCES users(id),
    role TEXT NOT NULL CHECK (role IN ('project_admin', 'certificate_admin', 'deployment_admin', 'dns_admin', 'audit_viewer', 'read_only')),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (project_id, user_id, role)
);
CREATE INDEX idx_project_members_user ON project_members(user_id, project_id);
