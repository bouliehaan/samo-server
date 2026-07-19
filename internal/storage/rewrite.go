package storage

import "strings"

// rewritePlaceholders converts SQLite-style `?` positional parameters into
// PostgreSQL-style `$1`, `$2`, ... parameters.
//
// This is the linchpin of dual-backend support: the entire codebase writes
// queries with `?` (1500+ call sites), and rewriting at the driver boundary
// lets every one of them run unchanged on PostgreSQL. A bug here silently
// corrupts queries, so the scanner is deliberately conservative — a `?` is
// only ever treated as a placeholder when it appears in ordinary SQL text,
// never inside:
//
//   - single-quoted string literals ('foo', with ” as the escaped quote)
//   - double-quoted identifiers ("weird column")
//   - dollar-quoted strings ($tag$ ... $tag$) — PostgreSQL-only, but handled
//     so a hand-written PG query never gets mangled
//   - line comments (-- ... end-of-line)
//   - block comments (/* ... */, non-nested like standard SQL)
//
// The SQL this codebase emits originates from SQLite, where `?` has no meaning
// other than "positional placeholder", so in practice only the plain-text case
// occurs; the literal/comment handling is belt-and-suspenders against future
// hand-written PostgreSQL.
//
// It also rewrites the `CURRENT_TIMESTAMP` keyword (again, only outside string
// literals/comments) into an equivalent RFC3339 text expression. SQLite's
// CURRENT_TIMESTAMP yields a text value, so the codebase writes it straight into
// TEXT columns; Postgres's CURRENT_TIMESTAMP is a timestamptz and cannot be
// assigned to text. Rewriting it here fixes ~90 inline write sites at once with
// the same portable RFC3339 format the schema defaults use.
//
// If the query contains neither a `?` nor `CURRENT_TIMESTAMP`, the original
// string is returned untouched with no allocation.
func rewritePlaceholders(query string) string {
	if !strings.ContainsRune(query, '?') && !containsFold(query, "CURRENT_TIMESTAMP") {
		return query
	}

	var b strings.Builder
	b.Grow(len(query) + 32)

	n := 0 // number of placeholders emitted so far
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch c {
		case '\'':
			// Single-quoted string literal. Copy verbatim until the closing
			// quote, honoring the SQL '' escape for an embedded quote.
			b.WriteByte(c)
			i++
			for i < len(query) {
				b.WriteByte(query[i])
				if query[i] == '\'' {
					if i+1 < len(query) && query[i+1] == '\'' {
						// Escaped quote: consume both, stay in the string.
						b.WriteByte(query[i+1])
						i++
					} else {
						break
					}
				}
				i++
			}
		case '"':
			// Double-quoted identifier. Same "" escape rule.
			b.WriteByte(c)
			i++
			for i < len(query) {
				b.WriteByte(query[i])
				if query[i] == '"' {
					if i+1 < len(query) && query[i+1] == '"' {
						b.WriteByte(query[i+1])
						i++
					} else {
						break
					}
				}
				i++
			}
		case '$':
			// Possible dollar-quoted string: $tag$ ... $tag$ (tag may be empty).
			if tag, ok := dollarTag(query, i); ok {
				b.WriteString(tag)
				i += len(tag)
				// Find the matching closing tag.
				if idx := strings.Index(query[i:], tag); idx >= 0 {
					b.WriteString(query[i : i+idx+len(tag)])
					i += idx + len(tag) - 1
				} else {
					// Unterminated — copy the rest and stop.
					b.WriteString(query[i:])
					i = len(query)
				}
			} else {
				b.WriteByte(c)
			}
		case '-':
			// Line comment: -- ... to end of line.
			if i+1 < len(query) && query[i+1] == '-' {
				if idx := strings.IndexByte(query[i:], '\n'); idx >= 0 {
					b.WriteString(query[i : i+idx+1])
					i += idx
				} else {
					b.WriteString(query[i:])
					i = len(query)
				}
			} else {
				b.WriteByte(c)
			}
		case '/':
			// Block comment: /* ... */ (standard SQL, non-nested).
			if i+1 < len(query) && query[i+1] == '*' {
				if idx := strings.Index(query[i:], "*/"); idx >= 0 {
					b.WriteString(query[i : i+idx+2])
					i += idx + 1
				} else {
					b.WriteString(query[i:])
					i = len(query)
				}
			} else {
				b.WriteByte(c)
			}
		case '?':
			n++
			b.WriteByte('$')
			b.WriteString(itoa(n))
		case 'C', 'c':
			// CURRENT_TIMESTAMP keyword (word-bounded) → RFC3339 text expression.
			if end, ok := matchKeyword(query, i, "CURRENT_TIMESTAMP"); ok {
				b.WriteString(pgCurrentTimestamp)
				i = end - 1
				continue
			}
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}

	return b.String()
}

// pgCurrentTimestamp is the Postgres replacement for the CURRENT_TIMESTAMP
// keyword: an RFC3339 UTC string, matching what the generated schema uses for
// its text timestamp defaults and what the Go code parses on read.
const pgCurrentTimestamp = `to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`

// matchKeyword reports whether kw (case-insensitive) appears at position i with
// SQL identifier boundaries on both sides, returning the index just past it.
func matchKeyword(q string, i int, kw string) (int, bool) {
	if i > 0 && isIdentByte(q[i-1]) {
		return 0, false
	}
	if i+len(kw) > len(q) || !strings.EqualFold(q[i:i+len(kw)], kw) {
		return 0, false
	}
	if end := i + len(kw); end < len(q) && isIdentByte(q[end]) {
		return 0, false
	}
	return i + len(kw), true
}

func isIdentByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

// containsFold reports whether s contains sub, case-insensitively, without
// allocating in the common no-match case.
func containsFold(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

// dollarTag returns the dollar-quote opening tag (e.g. "$$" or "$body$")
// beginning at position i, if the text there is a valid tag start. A tag is
// $, an optional identifier, then $. Anything else (e.g. a bare `$1` param, or
// `$` followed by a digit) is not a dollar quote.
func dollarTag(query string, i int) (string, bool) {
	if query[i] != '$' {
		return "", false
	}
	j := i + 1
	for j < len(query) {
		c := query[j]
		if c == '$' {
			return query[i : j+1], true
		}
		isIdent := c == '_' ||
			(c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9' && j > i+1)
		if !isIdent {
			return "", false
		}
		j++
	}
	return "", false
}

// itoa renders a small non-negative int without importing strconv into the
// hot query path. Placeholder counts are tiny.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
