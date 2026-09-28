package database

import (
	"context"
	"database/sql"
	"fmt"
)

// 以新增迁移修复已升级的数据库，不修改或重新执行第十一版迁移。
func installSQLiteReplacementGuards(ctx context.Context, tx *sql.Tx) error {
	for _, resource := range []struct{ table, column string }{{"domains", "domain_id"}, {"servers", "server_id"}} {
		invalidBinding := fmt.Sprintf(`EXISTS (SELECT 1 FROM domain_servers b LEFT JOIN %s r
			ON r.id = b.%s AND r.project_id = b.project_id WHERE r.id IS NULL)`, resource.table, resource.column)
		invalidProject := fmt.Sprintf(`EXISTS (SELECT 1 FROM %s r LEFT JOIN projects p
			ON p.id = r.project_id WHERE p.id IS NULL)`, resource.table)
		var invalid bool
		if err := tx.QueryRowContext(ctx, "SELECT "+invalidBinding+" OR "+invalidProject).Scan(&invalid); err != nil {
			return fmt.Errorf("检查资源引用失败：%w", err)
		}
		if invalid {
			return fmt.Errorf("%s 存在无效项目或部署引用，拒绝升级，请先核查原数据", resource.table)
		}
		statements := []string{
			// UPDATE OR REPLACE 可能删除冲突的另一行，不能只检查 OLD 所拥有的绑定。
			fmt.Sprintf(`CREATE TRIGGER scert_%[1]s_replace_update_guard BEFORE UPDATE ON %[1]s
				WHEN (NEW.id != OLD.id AND EXISTS (SELECT 1 FROM domain_servers WHERE %[2]s = NEW.id))
				OR (NEW.rowid != OLD.rowid AND EXISTS (SELECT 1 FROM %[1]s r
					JOIN domain_servers b ON b.%[2]s = r.id WHERE r.rowid = NEW.rowid))
				BEGIN SELECT RAISE(ABORT, '目标资源仍有部署关联，不能替换'); END`, resource.table, resource.column),
			// INSERT 前自动行号尚未确定。插入后验证引用，覆盖显式行号替换且不误拦自动行号。
			// 失败回滚整个外层语句，包括 REPLACE 的隐式删除；不依赖递归触发器开关。
			fmt.Sprintf(`CREATE TRIGGER scert_%s_replace_insert_guard AFTER INSERT ON %s
				WHEN %s BEGIN SELECT RAISE(ABORT, '替换操作破坏部署关联'); END`, resource.table, resource.table, invalidBinding),
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("建立资源替换保护失败：%w", err)
			}
		}
	}
	statements := []string{
		`CREATE TRIGGER scert_project_replace_insert_guard BEFORE INSERT ON projects
			WHEN EXISTS (SELECT 1 FROM domains WHERE project_id = NEW.id)
			OR EXISTS (SELECT 1 FROM servers WHERE project_id = NEW.id)
			BEGIN SELECT RAISE(ABORT, '项目仍被资源引用，不能替换'); END`,
		`CREATE TRIGGER scert_project_replace_update_guard BEFORE UPDATE ON projects
			WHEN (NEW.id != OLD.id AND (EXISTS (SELECT 1 FROM domains WHERE project_id = NEW.id)
				OR EXISTS (SELECT 1 FROM servers WHERE project_id = NEW.id)))
			OR (NEW.rowid != OLD.rowid AND EXISTS (SELECT 1 FROM projects p WHERE p.rowid = NEW.rowid
				AND (EXISTS (SELECT 1 FROM domains WHERE project_id = p.id)
				OR EXISTS (SELECT 1 FROM servers WHERE project_id = p.id))))
			BEGIN SELECT RAISE(ABORT, '目标项目仍被资源引用，不能替换'); END`,
		`CREATE TRIGGER scert_project_replace_reference_guard AFTER INSERT ON projects
			WHEN EXISTS (SELECT 1 FROM domains r LEFT JOIN projects p ON p.id = r.project_id WHERE p.id IS NULL)
			OR EXISTS (SELECT 1 FROM servers r LEFT JOIN projects p ON p.id = r.project_id WHERE p.id IS NULL)
			BEGIN SELECT RAISE(ABORT, '替换操作破坏项目引用'); END`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("建立项目替换保护失败：%w", err)
		}
	}
	return nil
}
