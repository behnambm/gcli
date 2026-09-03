// === Behavioral Contract: `gcli tables <sql-datasource>` ===
//   - Resolves the named datasource and queries information_schema.tables
//     through /api/ds/query with format=table
//   - Renders the returned table names as a single "table" column

// === Behavioral Contract: `gcli columns <sql-datasource> <table>` ===
//   - Rejects table identifiers that don't match ^[A-Za-z_][A-Za-z0-9_$]*$
//     (no empty, leading digit, spaces, quotes, or semicolons)
//   - Double-quotes the identifier (with "" escaping) and queries
//     information_schema.columns for column_name/data_type/is_nullable
//   - Renders the rows as column/type/nullable columns
package cmd

import (
	"strings"
	"testing"
)

const tablesDSQueryResponse = `{"results":{"A":{"status":200,"frames":[{"schema":{"refId":"A","fields":[{"name":"table","type":"string"}]},"data":{"values":[["invoices","users"]]}}]}}}`

const columnsDSQueryResponse = `{"results":{"A":{"status":200,"frames":[{"schema":{"refId":"A","fields":[{"name":"column","type":"string"},{"name":"type","type":"string"},{"name":"nullable","type":"string"}]},"data":{"values":[["id","total"],["integer","numeric"],["NO","NO"]]}}]}}}`

func TestTablesCommand_listsTableNames(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{
		"/api/datasources": defaultDatasourcesResponse,
		"/api/ds/query":    tablesDSQueryResponse,
	}, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	out := runCommand(t, "tables", "Billing")

	for _, want := range []string{"invoices", "users"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing table %q", out, want)
		}
	}
}

func TestTablesCommand_sendsInformationSchemaTablesQuery(t *testing.T) {
	var gotRaw string
	srv := fakeGrafana(t, map[string]string{
		"/api/datasources": defaultDatasourcesResponse,
		"/api/ds/query":    tablesDSQueryResponse,
	}, func(t *testing.T, body map[string]any) {
		gotRaw, _ = firstQueryObject(t, body)["rawSql"].(string)
	})
	defer srv.Close()
	setEnv(t, srv.URL)

	runCommand(t, "tables", "Billing")

	if !strings.Contains(gotRaw, "information_schema.tables") {
		t.Errorf("rawSql = %q, want information_schema.tables query", gotRaw)
	}
}

func TestColumnsCommand_listsColumns(t *testing.T) {
	var gotRaw string
	srv := fakeGrafana(t, map[string]string{
		"/api/datasources": defaultDatasourcesResponse,
		"/api/ds/query":    columnsDSQueryResponse,
	}, func(t *testing.T, body map[string]any) {
		gotRaw, _ = firstQueryObject(t, body)["rawSql"].(string)
	})
	defer srv.Close()
	setEnv(t, srv.URL)

	out := runCommand(t, "columns", "Billing", "invoices")

	for _, want := range []string{"id", "integer"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing %q", out, want)
		}
	}
	if !strings.Contains(gotRaw, `"invoices"`) {
		t.Errorf("rawSql = %q, want quoted identifier", gotRaw)
	}
}

func TestColumnsCommand_oneArg_usesDefaultDatasource(t *testing.T) {
	var gotRaw string
	srv := fakeGrafana(t, map[string]string{
		"/api/datasources": defaultDatasourcesResponse,
		"/api/ds/query":    columnsDSQueryResponse,
	}, func(t *testing.T, body map[string]any) {
		gotRaw, _ = firstQueryObject(t, body)["rawSql"].(string)
	})
	defer srv.Close()
	xdgEnv(t)
	runCommand(t, "profiles", "add", "p", "--url", srv.URL, "--token", "t", "--default-datasource", "Billing")
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_TOKEN", "")

	out := runCommand(t, "--profile", "p", "columns", "invoices")

	if !strings.Contains(out, "id") {
		t.Errorf("output = %q, missing column %q", out, "id")
	}
	if !strings.Contains(gotRaw, `"invoices"`) {
		t.Errorf("rawSql = %q, want quoted identifier", gotRaw)
	}
}

func TestColumnsCommand_rejectsBadIdentifier(t *testing.T) {
	srv := fakeGrafana(t, map[string]string{"/api/datasources": defaultDatasourcesResponse}, nil)
	defer srv.Close()
	setEnv(t, srv.URL)

	_, err := runCommandErr(t, "columns", "Billing", "invoices; DROP TABLE x")

	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("err = %v, want invalid-identifier error", err)
	}
}

func TestValidateIdent_valid(t *testing.T) {
	for _, ok := range []string{"invoices", "Orders_2026", "a$b"} {
		if err := validateIdent(ok); err != nil {
			t.Errorf("validateIdent(%q) = %v, want nil", ok, err)
		}
	}
}

func TestValidateIdent_invalid(t *testing.T) {
	for _, bad := range []string{"x;drop", "1abc", "a b", `a"b`, ""} {
		if err := validateIdent(bad); err == nil {
			t.Errorf("validateIdent(%q): want error", bad)
		}
	}
}

func TestQuoteIdent_escapesDoubleQuotes(t *testing.T) {
	if got := quoteIdent(`a"b`); got != `"a""b"` {
		t.Errorf("quoteIdent = %q, want %q", got, `"a""b"`)
	}
}
