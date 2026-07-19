package storage

import "testing"

func TestRewritePlaceholders(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"none", "SELECT 1", "SELECT 1"},
		{"single", "SELECT * FROM t WHERE id = ?", "SELECT * FROM t WHERE id = $1"},
		{
			"many",
			"INSERT INTO t (a,b,c) VALUES (?,?,?)",
			"INSERT INTO t (a,b,c) VALUES ($1,$2,$3)",
		},
		{
			"upsert",
			"INSERT INTO t (id,x) VALUES (?,?) ON CONFLICT(id) DO UPDATE SET x=excluded.x WHERE t.y=?",
			"INSERT INTO t (id,x) VALUES ($1,$2) ON CONFLICT(id) DO UPDATE SET x=excluded.x WHERE t.y=$3",
		},
		{
			"question mark inside single-quoted literal is not a placeholder",
			"SELECT * FROM t WHERE name = 'why?' AND id = ?",
			"SELECT * FROM t WHERE name = 'why?' AND id = $1",
		},
		{
			"escaped quote inside literal",
			"SELECT * FROM t WHERE a = 'it''s a ?' AND b = ?",
			"SELECT * FROM t WHERE a = 'it''s a ?' AND b = $1",
		},
		{
			"question mark inside quoted identifier",
			`SELECT "wat?" FROM t WHERE id = ?`,
			`SELECT "wat?" FROM t WHERE id = $1`,
		},
		{
			"line comment",
			"SELECT ? -- trailing ? comment\n, ?",
			"SELECT $1 -- trailing ? comment\n, $2",
		},
		{
			"block comment",
			"SELECT ? /* ? not a param ? */, ?",
			"SELECT $1 /* ? not a param ? */, $2",
		},
		{
			"like with escape",
			`SELECT * FROM t WHERE path LIKE ? ESCAPE '\'`,
			`SELECT * FROM t WHERE path LIKE $1 ESCAPE '\'`,
		},
		{
			"dollar quoted body untouched",
			"SELECT $$ has a ? inside $$, ?",
			"SELECT $$ has a ? inside $$, $1",
		},
		{
			"tagged dollar quote",
			"SELECT $body$ ? $body$ , ?",
			"SELECT $body$ ? $body$ , $1",
		},
		{
			"double digit placeholders",
			"VALUES (?,?,?,?,?,?,?,?,?,?,?,?)",
			"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)",
		},
		{
			"json path literal with question is safe",
			"SELECT json_extract(x, '$.a?b') FROM t WHERE id = ?",
			"SELECT json_extract(x, '$.a?b') FROM t WHERE id = $1",
		},
		{
			"current_timestamp keyword rewritten",
			"UPDATE t SET updated_at = CURRENT_TIMESTAMP WHERE id = ?",
			`UPDATE t SET updated_at = to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"') WHERE id = $1`,
		},
		{
			"current_timestamp lowercase + no placeholders",
			"UPDATE t SET a = current_timestamp",
			`UPDATE t SET a = to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		},
		{
			"current_timestamp inside string literal is left alone",
			"INSERT INTO t (a,b) VALUES ('CURRENT_TIMESTAMP', ?)",
			"INSERT INTO t (a,b) VALUES ('CURRENT_TIMESTAMP', $1)",
		},
		{
			"current_timestamp as part of an identifier is left alone",
			"SELECT current_timestamp_col FROM t WHERE id = ?",
			"SELECT current_timestamp_col FROM t WHERE id = $1",
		},
		{
			"two current_timestamps in one statement",
			"INSERT INTO t (a,b,c) VALUES (?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)",
			`INSERT INTO t (a,b,c) VALUES ($1, to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'))`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rewritePlaceholders(tc.in)
			if got != tc.want {
				t.Fatalf("rewritePlaceholders(%q)\n  got:  %q\n  want: %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRewritePlaceholdersRoundTripCount guards the invariant that the number of
// emitted $N equals the number of real placeholders, which is what pgx checks
// against the arg count.
func TestRewritePlaceholdersRoundTripCount(t *testing.T) {
	q := "INSERT INTO radio_stations (id,name,description,content_type,epoch,enabled,source,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name"
	got := rewritePlaceholders(q)
	// Highest placeholder must be $9.
	if want := "$9"; !contains(got, want) || contains(got, "$10") {
		t.Fatalf("expected 9 placeholders, got: %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
