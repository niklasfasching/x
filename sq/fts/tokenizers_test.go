//go:build fts5

package fts_test

import (
	"database/sql"
	"log"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	_ "github.com/niklasfasching/x/sq/fts"
)

type queryTest struct {
	count       int
	query, desc string
}

func TestHTMLTokenizer(t *testing.T) {
	testTokenizer(t, "html", []string{
		`<h1 class=foo>Hello World</h1> <p>This is a test of the <b>Go</b> language.</p>`,
		`<div><span>Another test</span> with mixed Case.</div>`,
		`<p>Ignore this: <script>alert("xss")</script> and this: <style>body{}</style></p>`,
	}, []queryTest{
		{1, "hello", "Should match word in h1"},
		{1, "language", "Should match word in p"},
		{1, "go", "Should match word in b"},
		{1, "another", "Should match word in span"},
		{1, "case", "Should match case-insensitively"},
		{2, "test", "Should match common word in two docs"},
		{0, "alert", "Should not match script content"},
		{0, "body", "Should not match style content"},
		{0, "p", "Should not match html tags"},
		{1, "hello AND go", "Should handle AND operator"},
		{2, "case OR hello", "Should handle OR operator"},
	})
}

func TestArrayTokenizer(t *testing.T) {
	testTokenizer(t, "json", []string{
		`["hello", "world", 123, true, 45.6]`,
		`["goodbye", "world", 99, false]`,
		`["a", "b", "c"]`,
		`{"foo": "bar", "baz": false, "bam": 42}`,
		`{"bam": 43}`,
	}, []queryTest{
		{2, "world", "Should match a common string token"},
		{1, "hello", "Should match a unique string token"},
		{1, "123", "Should match an integer token"},
		{1, "true", "Should match a boolean 'true' token"},
		{1, "false", "Should match a boolean 'false' token"},
		{1, `"45.6"`, "Should match a float token"},
		{1, "goodbye", "Should match another unique string token"},
		{0, "d", "Should not match a non-existent token"},
		{1, "world AND goodbye", "Should handle FTS5 AND operator"},
		{2, "hello OR 99", "Should handle FTS5 OR operator"},
		{1, `foo•bar`, "Should handle string•string"},
		{2, `bam•4*`, "Should handle prefix queries and numbers"},
		{1, `baz•false`, "Should handle bools"},
	})
}

func TestHighlight(t *testing.T) {
	db := newDB(t, "html", []string{
		`a b c d e f g<h1 class=foo>Hello World</h1>. <p>This is a test of the <b>Go</b> language.</p> a b c d e f g`,
	})
	q, s := `world test "the go"`, ""
	err := db.QueryRow("SELECT html_snippet(docs, 0) FROM docs WHERE docs MATCH ?", q).Scan(&s)
	if err != nil {
		t.Fatalf("Query %q failed: %v", q, err)
	}
	log.Println("RECEIVED", s, "FOR QUERY", q)
}

func newDB(t *testing.T, tokenizer string, docs []string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	schema := `CREATE VIRTUAL TABLE docs USING fts5(content, tokenize = '` + tokenizer + `')`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("Failed to apply schema: %v", err)
	}
	for i, doc := range docs {
		if _, err := db.Exec("INSERT INTO docs(rowid, content) VALUES (?, ?)", i+1, doc); err != nil {
			t.Fatalf("Failed to insert doc %d: %v", i+1, err)
		}
	}
	return db
}

func testTokenizer(t *testing.T, tokenizer string, docs []string, queries []queryTest) {
	t.Helper()
	db := newDB(t, tokenizer, docs)
	defer db.Close()
	for _, qt := range queries {
		t.Run(qt.desc, func(t *testing.T) {
			var count int
			err := db.QueryRow("SELECT count(*) FROM docs WHERE docs MATCH ?", qt.query).Scan(&count)
			if err != nil {
				t.Fatalf("Query %q failed: %v", qt.query, err)
			}
			if count != qt.count {
				t.Fatalf("Query %q: expected %d results, got %d", qt.query, qt.count, count)
			}
		})
	}
}
