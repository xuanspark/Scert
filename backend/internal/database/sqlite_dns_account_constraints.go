package database

import (
	"context"
	"database/sql"
	"fmt"
)

// 与已有资源约束一致，不依赖连接的外键开关，不修改用户的连接参数。
func installSQLiteDNSAccountConstraints(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TRIGGER scert_dns_accounts_project_insert BEFORE INSERT ON dns_accounts
		WHEN NOT EXISTS (SELECT 1 FROM projects WHERE id = NEW.project_id)
		BEGIN SELECT RAISE(ABORT, 'DNS 账户所属项目不存在'); END`,
		`CREATE TRIGGER scert_dns_accounts_project_update BEFORE UPDATE OF project_id ON dns_accounts
		WHEN NOT EXISTS (SELECT 1 FROM projects WHERE id = NEW.project_id)
		BEGIN SELECT RAISE(ABORT, 'DNS 账户所属项目不存在'); END`,
		`CREATE TRIGGER scert_dns_accounts_project_delete BEFORE DELETE ON projects
		WHEN EXISTS (SELECT 1 FROM dns_accounts WHERE project_id = OLD.id)
		BEGIN SELECT RAISE(ABORT, '项目仍有 DNS 账户，不能删除'); END`,
		`CREATE TRIGGER scert_dns_accounts_project_rekey BEFORE UPDATE OF id ON projects
		WHEN NEW.id <> OLD.id AND EXISTS (SELECT 1 FROM dns_accounts WHERE project_id = OLD.id)
		BEGIN SELECT RAISE(ABORT, '项目仍有 DNS 账户，不能更换标识'); END`,
		`CREATE TRIGGER scert_dns_accounts_project_replace BEFORE INSERT ON projects
		WHEN EXISTS (SELECT 1 FROM dns_accounts WHERE project_id = NEW.id)
		BEGIN SELECT RAISE(ABORT, '项目仍有 DNS 账户，不能替换'); END`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("安装 DNS 账户项目保护失败：%w", err)
		}
	}
	return nil
}
