package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
)

func TestSourceChangesScopedToResearch(t *testing.T) {
	pool := runCardsMock(t)
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(runCardsRunID).WillReturnRows(runCardsGetRunRow(runCardsRunID, ""))
	changed := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	pool.ExpectQuery("FROM evidence e JOIN nodes n").WithArgs(runCardsRunID).
		WillReturnRows(pgxmock.NewRows([]string{"source", "source_title", "node", "node_title", "evidence", "changed_at"}).
			AddRow(runCardsSourceID, "Источник", runCardsNodeID, "Задача", runCardsEvidenceID, changed))
	h := NewServer(NewPGStore(pool)).Routes()
	r := do(t, h, http.MethodGet, "/api/v1/runs/"+runCardsRunID+"/source-changes", "specialist", "")
	if r.Code != http.StatusOK {
		t.Fatalf("status %d: %s", r.Code, r.Body.String())
	}
	var items []SourceChange
	if err := json.Unmarshal(r.Body.Bytes(), &items); err != nil || len(items) != 1 || items[0].NodeID != runCardsNodeID || items[0].EvidenceID != runCardsEvidenceID || !items[0].ChangedAt.Equal(changed) {
		t.Fatalf("unexpected impact: %+v, %v", items, err)
	}
}
