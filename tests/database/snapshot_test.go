package database_test

import (
	"activity-descriptor/internal/store"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"activity-descriptor/internal/store/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type проверкаСнимка struct {
	RootNodeID string `json:"root_node_id"`
	Nodes      []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Evidence []struct {
			ID       string `json:"id"`
			Quote    string `json:"quote"`
			Approved bool   `json:"approved"`
			Source   struct {
				ID    string  `json:"id"`
				Type  string  `json:"type"`
				Title string  `json:"title"`
				URL   *string `json:"source_url"`
			} `json:"source"`
		} `json:"evidence"`
	} `json:"nodes"`
}

func TestСнимокСохраняетИсторию(t *testing.T) {
	url := os.Getenv("STORE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("для проверки нужна STORE_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var runID, sourceID, rootID pgtype.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('тест снимков') RETURNING id`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, runID)
	if err = pool.QueryRow(ctx, `INSERT INTO sources(run_id,type,title,source_url,content) VALUES($1,'document','исходное название','https://example.org/old','секретный полный текст') RETURNING id`, runID).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, runID)
		_, _ = pool.Exec(ctx, `DELETE FROM sources WHERE id=$1`, sourceID)
	}()
	setup, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer setup.Rollback(ctx)
	if err = setup.QueryRow(ctx, `INSERT INTO nodes(run_id,type,title) VALUES($1,'role','исходная роль') RETURNING id`, runID).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	if _, err = setup.Exec(ctx, `UPDATE runs SET root_node_id=$2 WHERE id=$1`, runID, rootID); err != nil {
		t.Fatal(err)
	}
	if _, err = setup.Exec(ctx, `INSERT INTO evidence(node_id,source_id,quote,approved) VALUES($1,$2,'первая цитата',true),($1,$2,'вторая цитата',false)`, rootID, sourceID); err != nil {
		t.Fatal(err)
	}
	if err = setup.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	s := store.New(pool)
	old, err := s.SaveSnapshot(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Version != 1 {
		t.Fatalf("версия: %d", old.Version)
	}
	var initial проверкаСнимка
	if err = json.Unmarshal(old.Tree, &initial); err != nil {
		t.Fatal(err)
	}
	if len(initial.Nodes) != 1 || initial.Nodes[0].Title != "исходная роль" || len(initial.Nodes[0].Evidence) != 2 {
		t.Fatalf("неверный снимок: %s", old.Tree)
	}
	for _, e := range initial.Nodes[0].Evidence {
		if e.Source.Title != "исходное название" || e.Source.Type != "document" || e.Source.URL == nil || *e.Source.URL != "https://example.org/old" || e.ID == "" || e.Source.ID == "" {
			t.Fatalf("неверное подтверждение: %+v", e)
		}
	}
	quotes := map[string]bool{}
	for _, e := range initial.Nodes[0].Evidence {
		quotes[e.Quote] = e.Approved
	}
	if len(quotes) != 2 || !quotes["первая цитата"] || quotes["вторая цитата"] {
		t.Fatalf("неверные цитаты или одобрения: %+v", quotes)
	}
	var all map[string]any
	if err = json.Unmarshal(old.Tree, &all); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(all)
	if strings.Contains(string(data), "секретный полный текст") {
		t.Fatal("полный текст попал в снимок")
	}
	if _, err = pool.Exec(ctx, `UPDATE nodes SET title='новая роль' WHERE id=$1`, rootID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE evidence SET quote='изменённая цитата',approved=false WHERE node_id=$1`, rootID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE sources SET title='новое название',source_url='https://example.org/new' WHERE id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}
	updated, err := s.SaveSnapshot(ctx, runID)
	if err != nil || updated.Version != 2 {
		t.Fatalf("второй снимок: %+v %v", updated, err)
	}
	var second проверкаСнимка
	if err = json.Unmarshal(updated.Tree, &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Nodes) != 1 || second.Nodes[0].Title != "новая роль" || len(second.Nodes[0].Evidence) != 2 || second.Nodes[0].Evidence[0].Quote != "изменённая цитата" || second.Nodes[0].Evidence[0].Source.Title != "новое название" {
		t.Fatalf("новая версия не содержит изменения: %s", updated.Tree)
	}
	var alternative pgtype.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO sources(type,title) VALUES('document','замена') RETURNING id`).Scan(&alternative); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, runID)
		_, _ = pool.Exec(ctx, `DELETE FROM sources WHERE id=$1`, alternative)
	}()
	if _, err = pool.Exec(ctx, `INSERT INTO evidence(node_id,source_id,quote) VALUES($1,$2,'новая цитата')`, rootID, alternative); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM evidence WHERE source_id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM sources WHERE id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}
	saved, err := s.GetSnapshot(ctx, db.GetSnapshotParams{RunID: runID, ID: old.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(old.Tree, saved.Tree) {
		t.Fatal("содержимое снимка изменилось")
	}
	var after проверкаСнимка
	if err = json.Unmarshal(saved.Tree, &after); err != nil {
		t.Fatal(err)
	}
	if len(after.Nodes) != 1 || len(after.Nodes[0].Evidence) != 2 || after.Nodes[0].Title != "исходная роль" || after.Nodes[0].Evidence[0].Source.Title != "исходное название" {
		t.Fatalf("история изменилась: %s", saved.Tree)
	}
	const parallel = 5
	var wg sync.WaitGroup
	errs := make(chan error, parallel)
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.SaveSnapshot(ctx, runID); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	snaps, err := s.ListSnapshotsByRun(ctx, runID)
	if err != nil || len(snaps) != parallel+2 {
		t.Fatalf("список: %d %v", len(snaps), err)
	}
	for i, snap := range snaps {
		if snap.Version != int32(i+1) {
			t.Fatalf("повтор или пропуск версии: %d", snap.Version)
		}
	}
	last := snaps[len(snaps)-1]
	var afterDelete проверкаСнимка
	if err = json.Unmarshal(last.Tree, &afterDelete); err != nil {
		t.Fatal(err)
	}
	if len(afterDelete.Nodes[0].Evidence) != 1 || afterDelete.Nodes[0].Evidence[0].Source.Title != "замена" {
		t.Fatal("удалённые подтверждения остались в новом снимке")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, runID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetSnapshot(ctx, db.GetSnapshotParams{RunID: runID, ID: old.ID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("снимок не удалён вместе с исследованием: %v", err)
	}
}

func TestСнимокБезКорня(t *testing.T) {
	url := os.Getenv("STORE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("для проверки нужна STORE_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var runID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('пустое исследование') RETURNING id`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, runID)
	s := store.New(pool)
	if _, err := s.SaveSnapshot(ctx, runID); err == nil {
		t.Fatal("для исследования без корня снимок должен отклоняться")
	}
	list, err := s.ListSnapshotsByRun(ctx, runID)
	if err != nil || len(list) != 0 {
		t.Fatalf("появился пустой снимок: %v %v", list, err)
	}
	if _, err := s.SaveSnapshot(ctx, pgtype.UUID{}); err == nil {
		t.Fatal("пустой идентификатор должен отклоняться")
	}
}
