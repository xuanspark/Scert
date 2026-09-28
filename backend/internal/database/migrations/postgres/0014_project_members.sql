CREATE TABLE project_members (
    project_id VARCHAR(36) NOT NULL REFERENCES projects(id),
    user_id VARCHAR(36) NOT NULL REFERENCES users(id),
    role VARCHAR(32) NOT NULL CHECK (role IN ('project_admin', 'certificate_admin', 'deployment_admin', 'dns_admin', 'audit_viewer', 'read_only')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (project_id, user_id, role)
);
CREATE INDEX idx_project_members_user ON project_members(user_id, project_id);
