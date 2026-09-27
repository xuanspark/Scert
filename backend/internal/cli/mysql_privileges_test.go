package cli

import "testing"

func TestMySQLTriggerGrantParsing(t *testing.T) {
	for _, item := range []struct {
		grant, schema, table string
		trigger              bool
	}{
		{"GRANT SELECT, TRIGGER ON `sample`.* TO 'account'@'localhost'", "sample", "*", true},
		{"GRANT ALL PRIVILEGES ON *.* TO 'account'@'localhost'", "*", "*", true},
		{"GRANT TRIGGER ON `sam``ple`.`ta``ble``` TO 'account'@'localhost'", "sam`ple", "ta`ble`", true},
		{"REVOKE TRIGGER ON `sample`.* FROM 'account'@'localhost'", "sample", "*", true},
		{"GRANT SELECT (`TRIGGER`) ON `sample`.`items` TO 'account'@'localhost'", "sample", "items", false},
	} {
		match := mysqlPrivilegeGrant.FindStringSubmatch(item.grant)
		if len(match) != 5 || unquoteMySQLGrantIdentifier(match[3]) != item.schema || unquoteMySQLGrantIdentifier(match[4]) != item.table || mysqlHasTriggerPrivilege(match[2]) != item.trigger {
			t.Fatalf("授权语句解析错误：%s", item.grant)
		}
	}
	if match := mysqlPrivilegeGrant.FindStringSubmatch("GRANT `TRIGGER`@`%` TO `account`@`localhost`"); match != nil {
		t.Fatal("不能把角色名称当成直接授予的对象权限")
	}
}
