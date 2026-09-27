package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestMySQLViewDependencies(t *testing.T) {
	for _, scenario := range []struct {
		name, query string
		want        []string
	}{
		{"simple", "select * from `z_base`", []string{".z_base"}},
		{"qualified", "select * from `db`.`z_base`", []string{"db.z_base"}},
		{"join", "select x.id from `a` x left join `b` y on x.id=y.id, `c`", []string{".a", ".b", ".c"}},
		{"parenthesized_join", "select * from (`a` join (`b` cross join `c`) on a.id=b.id)", []string{".a", ".b", ".c"}},
		{"subquery", "select (select id from `a`) from `b` where exists(select 1 from `c`)", []string{".a", ".b", ".c"}},
		{"derived", "select * from (select * from `a` union all select * from `b`) q join `c` on true", []string{".a", ".b", ".c"}},
		{"lateral", "select * from `a`, lateral (select * from `b`) q", []string{".a", ".b"}},
		{"function_from", "select extract(year from d), trim(both 'a' from x), substring(x from 2) from `base`", []string{".base"}},
		{"function_only", "select extract(year from d), trim('from z_base'), `z_base`(1)", nil},
		{"strings", "select 'from `a`', 'join c' from `d`", []string{".d"}},
		{"quoted_keywords", "select `from`, `join`, `select` from `from` as `join`", []string{".from"}},
		{"quoted_punctuation", "select * from `中文``(表).名`", []string{".中文`(表).名"}},
		{"comments", "select /* from `a` */ * from `b` -- join `c`\n # from `d`\n", []string{".b"}},
		{"hint", "select /*+ NO_MERGE(a) */ * from `b`", []string{".b"}},
		{"cte", "with a as (select * from `z`) select * from a", []string{".z"}},
		{"cte_chain", "with a(x) as (select id from `z`), b as (select * from a) select * from b join `q` on true", []string{".z", ".q"}},
		{"cte_shadow", "with a as (select * from a) select * from a", []string{".a"}},
		{"cte_qualified", "with a as (select 1) select * from `db`.`a`", []string{"db.a"}},
		{"cte_recursive", "with recursive a(n) as (select 1 union all select n+1 from a where n<3) select * from a", nil},
		{"cte_case", "with a as (select 1) select * from A", nil},
		{"cte_scope", "select * from (with a as (select * from z) select * from a) q join a on true", []string{".z", ".a"}},
		{"cte_inherited", "with a as (select * from z) select * from (select * from a) q", []string{".z"}},
		{"json_table", "select * from JSON_TABLE('[1]', '$[*]' columns (x int path '$')) j join z on true", []string{".z"}},
		{"window", "select row_number() over (partition by a order by b) from z window w as (order by a)", []string{".z"}},
		{"on_subquery", "select * from a join b on exists(select * from c) join d using(id)", []string{".a", ".b", ".c", ".d"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			tokens, err := lexMySQLView(scenario.query)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			err = walkMySQLViewQuery(tokens, nil, false, 0, func(schema, name string) { got = append(got, schema+"."+name) })
			if err != nil || !reflect.DeepEqual(got, scenario.want) {
				t.Fatalf("依赖识别错误：得到 %v，预期 %v，错误 %v", got, scenario.want, err)
			}
		})
	}
}

func TestMySQLViewDependencyOrder(t *testing.T) {
	view := func(name, query string) mysqlView {
		return mysqlView{name: name, definition: map[string]string{"Create View": "CREATE ALGORITHM=MERGE DEFINER=`view`@`localhost` SQL SECURITY INVOKER VIEW " + quoteMySQLIdentifier(name) + " AS " + query}}
	}
	for _, scenario := range []struct {
		name  string
		views []mysqlView
		lower int
		want  string
	}{
		{"chain", []mysqlView{view("a", "select * from z"), view("z", "select * from y"), view("y", "select 1")}, 0, "y,z,a"},
		{"foreign", []mysqlView{view("a", "select * from other.z"), view("z", "select 1")}, 0, "a,z"},
		{"same_schema", []mysqlView{view("a", "select * from current.z"), view("z", "select 1")}, 0, "z,a"},
		{"insensitive", []mysqlView{view("a", "select * from CURRENT.Z"), view("z", "select 1")}, 1, "z,a"},
		{"preserved_case", []mysqlView{view("a", "select * from CURRENT.Z"), view("z", "select 1")}, 2, "z,a"},
		{"sensitive", []mysqlView{view("a", "select * from Z"), view("z", "select 1")}, 0, "a,z"},
		{"diamond", []mysqlView{view("a", "select * from b join c using(id)"), view("b", "select * from d"), view("c", "select * from d"), view("d", "select 1")}, 0, "d,b,c,a"},
		{"cycle", []mysqlView{view("a", "select * from z"), view("z", "select * from a")}, 0, ""},
		{"self", []mysqlView{view("a", "select * from a")}, 0, ""},
		{"duplicate", []mysqlView{view("a", "select 1"), view("A", "select 1")}, 1, ""},
		{"invalid", []mysqlView{{name: "a", definition: map[string]string{"Create View": "CREATE VIEW a"}}}, 0, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ordered, err := orderMySQLViews(scenario.views, "current", scenario.lower)
			if scenario.want == "" {
				if err == nil {
					t.Fatal("无效依赖未被拒绝")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, item := range ordered {
				names = append(names, item.name)
			}
			if strings.Join(names, ",") != scenario.want {
				t.Fatalf("依赖排序错误：%v", names)
			}
		})
	}
}

func TestMySQLViewMalformedDefinition(t *testing.T) {
	for _, query := range []string{"select 'unterminated", "select `unterminated", "select /* unfinished", "select /*!50000 1 */", "select * from (a", "select * from a)", "select * from db.", "select * from", "with a select 1", "with a as (select 1", "with", "select * from ,"} {
		t.Run(query, func(t *testing.T) {
			tokens, err := lexMySQLView(query)
			if err == nil {
				err = walkMySQLViewQuery(tokens, nil, false, 0, func(string, string) {})
			}
			if err == nil {
				t.Fatal("格式不完整的依赖未被拒绝")
			}
		})
	}
}
