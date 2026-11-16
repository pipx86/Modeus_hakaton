package ingestion

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestФорматыТаксономий(t *testing.T) {
	for _, tc := range []struct{ format, data, name string }{
		{"json", `{"name":"Справочник","items":[{"code":"A","title":"Родитель"},{"code":"B","title":"Ребёнок","parent_code":"A"}]}`, ""},
		{"csv", "code,title,parent_code\nA,Родитель,\nB,Ребёнок,A\n", "Справочник"},
	} {
		data, err := ReadTaxonomy(strings.NewReader(tc.data), tc.format, tc.name)
		if err != nil || len(data.Items) != 2 {
			t.Fatalf("%+v %v", data, err)
		}
	}
	for _, data := range []string{
		`{"name":"Справочник","items":[{"code":"A","title":"X","parent_code":"B"},{"code":"B","title":"Y","parent_code":"A"}]}`,
		`{"name":"Справочник","items":[{"code":"A","title":"X"},{"code":"A","title":"Y"}]}`,
		`{"name":"Справочник","items":[{"code":"A","title":"X","parent_code":"missing"}]}`,
	} {
		if _, err := ReadTaxonomy(strings.NewReader(data), "json", ""); err == nil {
			t.Fatalf("ошибочный справочник принят: %s", data)
		}
	}
}

func TestПовторныйИмпортТаксономии(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна INGEST_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	key := fmt.Sprintf("taxonomy-test-%d", time.Now().UnixNano())
	defer func() {
		pool.Exec(ctx, "DELETE FROM taxonomies WHERE source_id IN (SELECT id FROM sources WHERE provider='file' AND external_id=$1)", key)
		pool.Exec(ctx, "DELETE FROM sources WHERE provider='file' AND external_id=$1", key)
	}()
	data, err := ReadTaxonomy(strings.NewReader(`{"name":"Справочник","items":[{"code":"A","title":"Родитель"},{"code":"B","title":"Потомок","parent_code":"A"}]}`), "json", "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := SaveTaxonomy(ctx, pool, key, data)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := SaveTaxonomy(ctx, pool, key, data)
	if err != nil || id != id2 {
		t.Fatalf("%s %s %v", id, id2, err)
	}
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM taxonomy_items WHERE taxonomy_id=$1", id).Scan(&count); err != nil || count != 2 {
		t.Fatalf("%d %v", count, err)
	}
}
