package database

import (
	"context"
	"database/sql"
	"fmt"
)

// 即使用户关闭外键或递归触发器，仍阻止跨项目绑定和父记录替换造成悬空引用。
func installSQLiteDomainDNSConstraints(ctx context.Context, tx *sql.Tx) error {
	for _, operation := range []string{"INSERT", "UPDATE"} {
		statement := fmt.Sprintf(`CREATE TRIGGER scert_domain_dns_%s BEFORE %s ON domain_dns_accounts
			WHEN NOT EXISTS (SELECT 1 FROM domains WHERE id = NEW.domain_id AND project_id = NEW.project_id)
			OR NOT EXISTS (SELECT 1 FROM dns_accounts WHERE id = NEW.dns_account_id AND project_id = NEW.project_id)
			BEGIN SELECT RAISE(ABORT, 'domain DNS project mismatch'); END`, operation, operation)
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	for _, parent := range []struct{ table, column, unique string }{
		{"domains", "domain_id", "name"}, {"dns_accounts", "dns_account_id", "credential_ref"},
	} {
		statements := []string{
			fmt.Sprintf(`CREATE TRIGGER scert_domain_dns_%s_delete BEFORE DELETE ON %s
				WHEN EXISTS (SELECT 1 FROM domain_dns_accounts WHERE %s = OLD.id)
				BEGIN SELECT RAISE(ABORT, 'domain DNS reference exists'); END`, parent.table, parent.table, parent.column),
			fmt.Sprintf(`CREATE TRIGGER scert_domain_dns_%s_update BEFORE UPDATE OF id, project_id ON %s
				WHEN (NEW.id <> OLD.id OR NEW.project_id <> OLD.project_id)
				AND EXISTS (SELECT 1 FROM domain_dns_accounts WHERE %s = OLD.id)
				BEGIN SELECT RAISE(ABORT, 'domain DNS reference exists'); END`, parent.table, parent.table, parent.column),
			fmt.Sprintf(`CREATE TRIGGER scert_domain_dns_%s_replace BEFORE INSERT ON %s
				WHEN EXISTS (SELECT 1 FROM domain_dns_accounts binding JOIN %s parent ON parent.id = binding.%s
					WHERE parent.id = NEW.id OR parent.%s = NEW.%s)
				BEGIN SELECT RAISE(ABORT, 'domain DNS replacement denied'); END`, parent.table, parent.table, parent.table, parent.column, parent.unique, parent.unique),
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	}
	return nil
}
