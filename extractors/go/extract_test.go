package main

import (
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestParseTables(t *testing.T) {
	sql := `
CREATE TABLE IF NOT EXISTS shops (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    owner_id BIGINT REFERENCES users(id),
    created_at TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (id),
    CONSTRAINT uq_name UNIQUE (name)
);

CREATE TABLE "points" (
    shop_id bigint,
    amount numeric(10,2)
);`
	tables := ParseTables(sql)
	if len(tables) != 2 {
		t.Fatalf("tables = %d, want 2", len(tables))
	}
	if tables[0].Name != "shops" {
		t.Errorf("first table = %q, want shops", tables[0].Name)
	}
	want := []string{"id:bigserial", "name:text", "owner_id:bigint", "created_at:timestamptz"}
	if len(tables[0].Columns) != len(want) {
		t.Fatalf("columns = %v, want %v (constraints must be skipped)", tables[0].Columns, want)
	}
	for i, c := range want {
		if tables[0].Columns[i] != c {
			t.Errorf("column[%d] = %q, want %q", i, tables[0].Columns[i], c)
		}
	}
	// numeric(10,2) must not be split on its inner comma.
	if len(tables[1].Columns) != 2 || tables[1].Columns[1] != "amount:numeric(10,2)" {
		t.Errorf("points columns = %v, want amount:numeric(10,2) intact", tables[1].Columns)
	}
}

func TestExtractRoutesGinWithGroupsAndHandlers(t *testing.T) {
	src := `package server

import "github.com/gin-gonic/gin"

func handleReview() gin.HandlerFunc { return nil }

type Adapter struct{}
func (a Adapter) WebHookApple(c *gin.Context) {}

func setup(r *gin.Engine, a Adapter) {
	r.GET("/version", func(c *gin.Context) {})
	r.GET("/r/:token", handleReview())
	r.POST("/webhook/apple", a.WebHookApple)
	api := r.Group("/api")
	v1 := api.Group("/v1")
	v1.GET("/shops", func(c *gin.Context) {})
	r.Handle("PUT", "/things/:id", func(c *gin.Context) {})
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module srv\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
		Dir: dir, Fset: fset,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	routes := ExtractRoutes(pkgs, fset, dir)

	got := map[string]string{} // "METHOD path" -> handler
	for _, r := range routes {
		got[r.Method+" "+r.Path] = r.Handler
	}
	cases := map[string]string{
		"GET /version":      "closure",
		"GET /r/:token":     "handleReview",
		"GET /api/v1/shops": "closure",
		"PUT /things/:id":   "closure",
	}
	for key, wantHandler := range cases {
		h, ok := got[key]
		if !ok {
			t.Errorf("route %q not detected (got %v)", key, keys(got))
			continue
		}
		if wantHandler == "handleReview" && h != "server.handleReview" && h != "handleReview" {
			t.Errorf("route %q handler = %q, want handleReview", key, h)
		}
	}
	// The method-value handler must resolve to the receiver type.
	if h := got["POST /webhook/apple"]; h != "server.Adapter.WebHookApple" && h != "Adapter.WebHookApple" {
		t.Errorf("POST /webhook/apple handler = %q, want ...Adapter.WebHookApple", h)
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
