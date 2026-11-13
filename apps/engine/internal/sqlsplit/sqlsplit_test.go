package sqlsplit

import (
	"reflect"
	"testing"
)

func TestStatements(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"simple", "CREATE TABLE a(x); INSERT INTO a VALUES (1);", []string{"CREATE TABLE a(x);", "INSERT INTO a VALUES (1);"}},
		{"no final semicolon", "SELECT 1; SELECT 2", []string{"SELECT 1;", "SELECT 2"}},
		{"quotes", `INSERT INTO a VALUES ('a;b', "c;d", [e;f], ` + "`g;h`" + `);`, []string{`INSERT INTO a VALUES ('a;b', "c;d", [e;f], ` + "`g;h`" + `);`}},
		{"escaped quote", "INSERT INTO a VALUES ('it''s; fine');", []string{"INSERT INTO a VALUES ('it''s; fine');"}},
		{"comments", "-- one; two\nSELECT 1; /* three; */ SELECT 2;", []string{"-- one; two\nSELECT 1;", "/* three; */ SELECT 2;"}},
		{"only comments dropped", "SELECT 1;\n-- trailing\n", []string{"SELECT 1;"}},
		{"drizzle breakpoint", "CREATE TABLE a(x);\n--> statement-breakpoint\nCREATE INDEX i ON a(x);", []string{"CREATE TABLE a(x);", "--> statement-breakpoint\nCREATE INDEX i ON a(x);"}},
		{"trigger", "CREATE TRIGGER t AFTER INSERT ON a BEGIN INSERT INTO b VALUES (1); UPDATE c SET x = 1; END; SELECT 1;",
			[]string{"CREATE TRIGGER t AFTER INSERT ON a BEGIN INSERT INTO b VALUES (1); UPDATE c SET x = 1; END;", "SELECT 1;"}},
		{"temp trigger lowercase", "create temp trigger t after delete on a begin select 1; end;", []string{"create temp trigger t after delete on a begin select 1; end;"}},
		{"END in a string inside a trigger", "CREATE TRIGGER t AFTER INSERT ON a BEGIN INSERT INTO b VALUES ('END;'); END;",
			[]string{"CREATE TRIGGER t AFTER INSERT ON a BEGIN INSERT INTO b VALUES ('END;'); END;"}},
	}
	for _, c := range cases {
		if got := Statements(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}
