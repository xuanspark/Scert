package database

import (
	"context"
	"database/sql"
	"fmt"
)

// SQLite 不重建业务表，以触发器实现引用约束；即使连接关闭外键开关也不能绕过。
// 触发器作为完整语句执行，不能用迁移文件的分号拆分器切开触发器主体。
func installSQLiteResourceConstraints(ctx context.Context, tx *sql.Tx) error {
	for _, resource := range []struct{ table, column string }{{"domains", "domain_id"}, {"servers", "server_id"}} {
		var invalid int
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s r LEFT JOIN projects p ON p.id = r.project_id WHERE p.id IS NULL`, resource.table)).Scan(&invalid); err != nil {
			return err
		}
		if invalid != 0 {
			return fmt.Errorf("%s 引用了不存在的项目，拒绝迁移", resource.table)
		}
		query := fmt.Sprintf(`SELECT COUNT(*) FROM domain_servers b LEFT JOIN %s r
			ON r.id = b.%s AND r.project_id = b.project_id WHERE r.id IS NULL`, resource.table, resource.column)
		if err := tx.QueryRowContext(ctx, query).Scan(&invalid); err != nil {
			return err
		}
		if invalid != 0 {
			return fmt.Errorf("历史域名服务器关联存在无效的 %s 引用，拒绝迁移，请先核查原数据", resource.table)
		}
		statements := []string{
			fmt.Sprintf(`CREATE TRIGGER scert_%[1]s_replace_guard BEFORE INSERT ON %[1]s
				WHEN EXISTS (SELECT 1 FROM domain_servers WHERE %[2]s = NEW.id)
				BEGIN SELECT RAISE(ABORT, '资源仍有部署关联，不能替换'); END`, resource.table, resource.column),
			fmt.Sprintf(`CREATE TRIGGER scert_%[1]s_project_insert BEFORE INSERT ON %[1]s
				WHEN NOT EXISTS (SELECT 1 FROM projects WHERE id = NEW.project_id)
				BEGIN SELECT RAISE(ABORT, '资源项目不存在'); END`, resource.table),
			fmt.Sprintf(`CREATE TRIGGER scert_%[1]s_project_update BEFORE UPDATE OF project_id, id ON %[1]s
				WHEN NOT EXISTS (SELECT 1 FROM projects WHERE id = NEW.project_id)
				OR EXISTS (SELECT 1 FROM domain_servers WHERE %[2]s = OLD.id
					AND (project_id != NEW.project_id OR %[2]s != NEW.id))
				BEGIN SELECT RAISE(ABORT, '资源项目不存在或仍有部署关联'); END`, resource.table, resource.column),
			fmt.Sprintf(`CREATE TRIGGER scert_%[1]s_binding_delete BEFORE DELETE ON %[1]s
				WHEN EXISTS (SELECT 1 FROM domain_servers WHERE %[2]s = OLD.id)
				BEGIN SELECT RAISE(ABORT, '资源仍有部署关联'); END`, resource.table, resource.column),
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("建立资源项目约束失败：%w", err)
			}
		}
	}
	// REPLACE 的隐式删除在关闭 recursive_triggers 时不触发删除保护，需保护唯一域名冲突。
	for _, event := range []string{"INSERT", "UPDATE"} {
		statement := fmt.Sprintf(`CREATE TRIGGER scert_domain_name_replace_%s BEFORE %s ON domains
			WHEN EXISTS (SELECT 1 FROM domains d JOIN domain_servers b ON b.domain_id = d.id
				WHERE d.name = NEW.name AND d.id != NEW.id)
			BEGIN SELECT RAISE(ABORT, '同名域名仍有部署关联，不能替换'); END`, event, event)
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("建立域名替换保护失败：%w", err)
		}
	}
	for _, event := range []string{"INSERT", "UPDATE"} {
		statement := fmt.Sprintf(`CREATE TRIGGER scert_binding_project_%s BEFORE %s ON domain_servers
			WHEN NOT EXISTS (SELECT 1 FROM domains WHERE id = NEW.domain_id AND project_id = NEW.project_id)
			OR NOT EXISTS (SELECT 1 FROM servers WHERE id = NEW.server_id AND project_id = NEW.project_id)
			BEGIN SELECT RAISE(ABORT, '域名与服务器必须存在且属于同一项目'); END`, event, event)
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("建立部署关联约束失败：%w", err)
		}
	}
	for _, event := range []string{"DELETE", "UPDATE OF id"} {
		name := "delete"
		condition := ""
		if event != "DELETE" {
			name = "update"
			condition = "OLD.id != NEW.id AND "
		}
		statement := fmt.Sprintf(`CREATE TRIGGER scert_project_resource_%s BEFORE %s ON projects
			WHEN %s(EXISTS (SELECT 1 FROM domains WHERE project_id = OLD.id)
			OR EXISTS (SELECT 1 FROM servers WHERE project_id = OLD.id))
			BEGIN SELECT RAISE(ABORT, '项目仍被资源引用'); END`, name, event, condition)
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("建立项目引用保护失败：%w", err)
		}
	}
	return nil
}
