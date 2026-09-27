package cli

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestMySQLRealOfficialInteroperability(t *testing.T) {
	server := mysqltest.New(t)
	fixtures := []struct {
		name   string
		create string
		insert string
		query  string
		want   string
	}{
		{
			name:   "basic_values",
			create: "CREATE TABLE interop (id INT PRIMARY KEY, label TEXT, payload BLOB, amount DECIMAL(20,6), optional_value TEXT NULL) ENGINE=InnoDB",
			insert: "INSERT INTO interop VALUES (1, '证书;单引号''与反斜线\\\\', X'00013B5CFF', 123456789.123456, NULL)",
			query:  "SELECT CONCAT(label, '|', HEX(payload), '|', CAST(amount AS CHAR), '|', optional_value IS NULL) FROM interop WHERE id = 1",
			want:   "证书;单引号'与反斜线\\|00013B5CFF|123456789.123456|1",
		},
		{
			name:   "generated_columns",
			create: "CREATE TABLE interop (id INT PRIMARY KEY, base_value DECIMAL(20,6), stored_value DECIMAL(21,6) GENERATED ALWAYS AS (base_value * 2) STORED, virtual_value VARCHAR(80) GENERATED ALWAYS AS (CONCAT('证书;', id)) VIRTUAL, label TEXT) ENGINE=InnoDB",
			insert: "INSERT INTO interop (id, base_value, label) VALUES (7, 1234.123456, '保留原值')",
			query:  "SELECT CONCAT(CAST(base_value AS CHAR), '|', CAST(stored_value AS CHAR), '|', virtual_value, '|', label) FROM interop WHERE id = 7",
			want:   "1234.123456|2468.246912|证书;7|保留原值",
		},
		{
			name:   "only_generated_columns",
			create: "CREATE TABLE interop (stored_value INT GENERATED ALWAYS AS (42) STORED) ENGINE=InnoDB",
			insert: "INSERT INTO interop () VALUES (), (), ()",
			query:  "SELECT CONCAT(COUNT(*), '|', SUM(stored_value)) FROM interop",
			want:   "3|126",
		},
	}
	if server.IsMySQL8() {
		fixtures = append(fixtures, struct {
			name   string
			create string
			insert string
			query  string
			want   string
		}{
			name:   "invisible_column",
			create: "CREATE TABLE interop (id INT PRIMARY KEY, hidden_value VARCHAR(80) INVISIBLE DEFAULT '默认隐藏值', label TEXT) ENGINE=InnoDB",
			insert: "INSERT INTO interop (id, hidden_value, label) VALUES (1, '隐藏列实际值', '可见列')",
			query:  "SELECT CONCAT(hidden_value, '|', label) FROM interop WHERE id = 1",
			want:   "隐藏列实际值|可见列",
		})
	}
	for _, fixture := range fixtures {
		for _, direction := range []string{"scert_to_official", "official_to_scert"} {
			t.Run(fixture.name+"/"+direction, func(t *testing.T) {
				source := server.Database(t)
				db, err := database.Open(t.Context(), source)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				for _, statement := range []string{fixture.create, fixture.insert} {
					if _, err := db.DB.ExecContext(t.Context(), statement); err != nil {
						t.Fatal(err)
					}
				}
				checkMySQLInteropValue(t, source, fixture.query, fixture.want)
				definition := readMySQLInteropDefinition(t, db.DB)
				output := filepath.Join(server.Root(), fixture.name+"-"+direction+".sql")
				target := server.Database(t)
				if direction == "scert_to_official" {
					if err := backupMySQLDatabase(t.Context(), source, output); err != nil {
						t.Fatal(err)
					}
					server.Import(t, target, output)
				} else {
					server.Dump(t, source, output)
					if err := restoreMySQL(t.Context(), target, output); err != nil {
						t.Fatal(err)
					}
				}
				checkMySQLInteropValue(t, target, fixture.query, fixture.want)
				verify, err := database.Open(t.Context(), target)
				if err != nil {
					t.Fatal(err)
				}
				defer verify.Close()
				restoredDefinition := readMySQLInteropDefinition(t, verify.DB)
				if restoredDefinition != definition {
					t.Fatalf("互操作恢复后表结构不一致：\n源表：%s\n目标表：%s", definition, restoredDefinition)
				}
			})
		}
	}
}

func readMySQLInteropDefinition(t *testing.T, db *sql.DB) string {
	t.Helper()
	queries := []string{
		`SELECT TABLE_TYPE, ENGINE, TABLE_COLLATION, ROW_FORMAT, CREATE_OPTIONS
		 FROM information_schema.TABLES
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'interop'`,
		`SELECT COLUMN_NAME, ORDINAL_POSITION, COLUMN_DEFAULT, IS_NULLABLE,
		 COLUMN_TYPE, CHARACTER_SET_NAME, COLLATION_NAME, EXTRA,
		 GENERATION_EXPRESSION, COLUMN_COMMENT
		 FROM information_schema.COLUMNS
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'interop'
		 ORDER BY ORDINAL_POSITION`,
		`SELECT INDEX_NAME, NON_UNIQUE, SEQ_IN_INDEX, COLUMN_NAME,
		 COLLATION, SUB_PART, PACKED, NULLABLE, INDEX_TYPE, COMMENT, INDEX_COMMENT
		 FROM information_schema.STATISTICS
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'interop'
		 ORDER BY INDEX_NAME, SEQ_IN_INDEX`,
	}
	var metadata [][][]sql.NullString
	for _, query := range queries {
		rows, err := db.QueryContext(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var section [][]sql.NullString
		for rows.Next() {
			values := make([]sql.NullString, len(columns))
			pointers := make([]any, len(values))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			section = append(section, values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		metadata = append(metadata, section)
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func checkMySQLInteropValue(t *testing.T, cfg config.DatabaseConfig, query, want string) {
	t.Helper()
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.DB.QueryRowContext(t.Context(), query).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != want {
		t.Fatalf("官方工具互操作后数据不一致：得到 %q，期望 %q", value, want)
	}
}
