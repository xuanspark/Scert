package database

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
)

var mysqlMetadataIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

func installMySQLProjectMembers(ctx context.Context, db *sql.DB) error {
	var charset, collation, dataType string
	var length int
	if err := db.QueryRowContext(ctx, `SELECT CHARACTER_SET_NAME, COLLATION_NAME, DATA_TYPE, CHARACTER_MAXIMUM_LENGTH
		FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'users' AND column_name = 'id'`).
		Scan(&charset, &collation, &dataType, &length); err != nil {
		return fmt.Errorf("读取用户主键类型失败：%w", err)
	}
	if dataType != "varchar" || length != 36 || !mysqlMetadataIdentifier.MatchString(charset) || !mysqlMetadataIdentifier.MatchString(collation) {
		return fmt.Errorf("用户主键类型不兼容，拒绝建立项目成员关系")
	}
	// MySQL 历史库的默认字符集可能改变，不能用当前默认值猜测用户主键类型。
	statement := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS project_members (
		project_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
		user_id VARCHAR(36) CHARACTER SET %s COLLATE %s NOT NULL,
		role ENUM('project_admin', 'certificate_admin', 'deployment_admin', 'dns_admin', 'audit_viewer', 'read_only') NOT NULL,
		created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
		PRIMARY KEY (project_id, user_id, role),
		KEY idx_project_members_user (user_id, project_id),
		CONSTRAINT fk_project_members_project FOREIGN KEY (project_id) REFERENCES projects(id),
		CONSTRAINT fk_project_members_user FOREIGN KEY (user_id) REFERENCES users(id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`, charset, collation)
	if _, err := db.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("建立项目成员关系失败：%w", err)
	}
	// 重试只接受完整的既有表；不能把同名残缺表当作迁移成功。
	var userCharset, userCollation, roleType string
	if err := db.QueryRowContext(ctx, `SELECT CHARACTER_SET_NAME, COLLATION_NAME FROM information_schema.columns
		WHERE table_schema=DATABASE() AND table_name='project_members' AND column_name='user_id'`).Scan(&userCharset, &userCollation); err != nil {
		return fmt.Errorf("检查成员用户字段失败：%w", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COLUMN_TYPE FROM information_schema.columns
		WHERE table_schema=DATABASE() AND table_name='project_members' AND column_name='role'`).Scan(&roleType); err != nil {
		return fmt.Errorf("检查成员角色字段失败：%w", err)
	}
	var references, primaryColumns int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.key_column_usage
		WHERE table_schema=DATABASE() AND table_name='project_members' AND referenced_table_schema=DATABASE()
		AND ((column_name='project_id' AND referenced_table_name='projects' AND referenced_column_name='id')
		OR (column_name='user_id' AND referenced_table_name='users' AND referenced_column_name='id'))`).Scan(&references); err != nil {
		return fmt.Errorf("检查成员外键失败：%w", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.key_column_usage
		WHERE table_schema=DATABASE() AND table_name='project_members' AND constraint_name='PRIMARY'
		AND ((ordinal_position=1 AND column_name='project_id') OR (ordinal_position=2 AND column_name='user_id')
		OR (ordinal_position=3 AND column_name='role'))`).Scan(&primaryColumns); err != nil {
		return fmt.Errorf("检查成员主键失败：%w", err)
	}
	if userCharset != charset || userCollation != collation || references != 2 || primaryColumns != 3 ||
		roleType != "enum('project_admin','certificate_admin','deployment_admin','dns_admin','audit_viewer','read_only')" {
		return fmt.Errorf("既有项目成员表结构不兼容，拒绝登记迁移版本")
	}
	return nil
}

func installSQLiteProjectMemberGuards(ctx context.Context, tx *sql.Tx) error {
	// 与既有资源约束一致，不依赖连接级外键和递归触发器开关。
	for _, event := range []string{"INSERT", "UPDATE"} {
		statement := fmt.Sprintf(`CREATE TRIGGER scert_members_reference_%s BEFORE %s ON project_members
			WHEN NOT EXISTS (SELECT 1 FROM projects WHERE id = NEW.project_id)
			OR NOT EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id)
			BEGIN SELECT RAISE(ABORT, '项目或用户不存在'); END`, event, event)
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("建立成员引用检查失败：%w", err)
		}
	}
	for _, parent := range []struct{ table, column string }{{"projects", "project_id"}, {"users", "user_id"}} {
		// 用户名、邮箱等唯一键替换也可能隐式删除另一个账户；成员仍在时禁止该替换。
		conflict := "p.id = NEW.id OR p.rowid = NEW.rowid"
		// 未显式指定行号时，插入前 NEW.rowid 可为 -1；不能误拒绝正常新增。
		insertConflict := "p.id = NEW.id OR (NEW.rowid != -1 AND p.rowid = NEW.rowid)"
		if parent.table == "users" {
			conflict += " OR p.username = NEW.username OR (NEW.email != '' AND p.email = NEW.email)"
			insertConflict += " OR p.username = NEW.username OR (NEW.email != '' AND p.email = NEW.email)"
		}
		statements := []string{
			fmt.Sprintf(`CREATE TRIGGER scert_members_%s_delete BEFORE DELETE ON %s
				WHEN EXISTS (SELECT 1 FROM project_members WHERE %s = OLD.id)
				BEGIN SELECT RAISE(ABORT, '请先撤销项目成员关系'); END`, parent.table, parent.table, parent.column),
			fmt.Sprintf(`CREATE TRIGGER scert_members_%s_insert BEFORE INSERT ON %s
				WHEN EXISTS (SELECT 1 FROM %s p JOIN project_members m ON m.%s = p.id WHERE %s)
				BEGIN SELECT RAISE(ABORT, '不能替换已有项目成员关联的记录'); END`, parent.table, parent.table, parent.table, parent.column, insertConflict),
			fmt.Sprintf(`CREATE TRIGGER scert_members_%s_update BEFORE UPDATE ON %s
				WHEN (NEW.id != OLD.id AND EXISTS (SELECT 1 FROM project_members WHERE %s = OLD.id))
				OR EXISTS (SELECT 1 FROM %s p JOIN project_members m ON m.%s = p.id WHERE p.id != OLD.id AND (%s))
				BEGIN SELECT RAISE(ABORT, '不能改变项目成员关联的记录标识'); END`, parent.table, parent.table, parent.column, parent.table, parent.column, conflict),
			fmt.Sprintf(`CREATE TRIGGER scert_members_%s_after_insert AFTER INSERT ON %s
				WHEN EXISTS (SELECT 1 FROM project_members m LEFT JOIN %s p ON p.id = m.%s WHERE p.id IS NULL)
				BEGIN SELECT RAISE(ABORT, '替换操作破坏项目成员关系'); END`, parent.table, parent.table, parent.table, parent.column),
			fmt.Sprintf(`CREATE TRIGGER scert_members_%s_after_update AFTER UPDATE ON %s
				WHEN EXISTS (SELECT 1 FROM project_members m LEFT JOIN %s p ON p.id = m.%s WHERE p.id IS NULL)
				BEGIN SELECT RAISE(ABORT, '替换操作破坏项目成员关系'); END`, parent.table, parent.table, parent.table, parent.column),
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("建立成员父记录保护失败：%w", err)
			}
		}
	}
	return nil
}
