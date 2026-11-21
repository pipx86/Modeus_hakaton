package api

import (
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"context"
	"encoding/json"
	"github.com/pashagolub/pgxmock/v4"
	"net/http"
	"testing"
)

type skillMockLLM struct{ answer string }

func (m skillMockLLM) Chat(context.Context, []llm.Message) (string, error) { return m.answer, nil }

func TestSkillHTTPExtractionPreview(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "preview only", true: "reject hallucinated evidence"}[foreign], func(t *testing.T) {
			pool := runCardsMock(t)
			pool.ExpectQuery("FROM runs WHERE id=").WithArgs(runCardsRunID).WillReturnRows(runCardsGetRunRow(runCardsRunID, ""))
			pool.ExpectQuery("SELECT payload FROM skill_model_revisions").WithArgs(runCardsRunID, 0).WillReturnRows(pgxmock.NewRows([]string{"payload"}))
			pool.ExpectQuery("FROM runs WHERE id=").WithArgs(runCardsRunID).WillReturnRows(runCardsGetRunRow(runCardsRunID, ""))
			evidence, _ := json.Marshal([]domain.Evidence{{ID: "e", SourceID: "s", RefID: "fragment", Quote: "Rollback transaction", Status: domain.EvidencePending}})
			pool.ExpectQuery("FROM nodes n WHERE n.run_id").WithArgs(runCardsRunID).WillReturnRows(pgxmock.NewRows([]string{"id", "type", "title", "description", "parent", "evidence", "zun", "review_status", "comment", "at"}).AddRow("n", domain.NodeTask, "Transaction", "", nil, evidence, []byte("null"), domain.NodeReviewPending, "", nil))
			model := domain.EmptySkillModel()
			model.Definitions = []domain.SkillDefinition{{ID: "d", Version: 1, Title: "Transactions", Kind: "knowledge", Tools: []domain.SkillToolUse{}}}
			evidenceID := "e"
			if foreign {
				evidenceID = "hallucinated"
			}
			model.Links = []domain.SkillNodeLink{{ID: "link", NodeID: "n", SkillRef: domain.SkillRef{ID: "d", Version: 1}, EvidenceIDs: []string{evidenceID}, Basis: "requirement", Reason: "Source describes rollback", Status: "approved"}}
			answer, _ := json.Marshal(model)
			server := NewServer(NewPGStore(pool))
			server.SetProgramMatcher(skillMockLLM{string(answer)})
			rec := do(t, server.Routes(), http.MethodPost, "/api/v1/runs/"+runCardsRunID+"/skill-model/extract", "user", "")
			if foreign {
				if rec.Code != 502 {
					t.Fatal(rec.Code, rec.Body.String())
				}
				return
			}
			if rec.Code != 200 {
				t.Fatal(rec.Code, rec.Body.String())
			}
			var candidate domain.SkillModel
			_ = json.Unmarshal(rec.Body.Bytes(), &candidate)
			if candidate.Revision != 0 || candidate.Links[0].Status != "proposed" {
				t.Fatal(rec.Body.String())
			}
			// No INSERT/UPDATE expectations: extraction must remain a preview.
		})
	}
}

func TestSkillHTTPReadAndAuth(t *testing.T) {
	for _, path := range []string{"skill-model", "skill-model/nodes", "skill-model/compare", "skill-model/extract"} {
		method := http.MethodGet
		if path == "skill-model/compare" || path == "skill-model/extract" {
			method = http.MethodPost
		}
		rec := do(t, NewServer(newMemStore()).Routes(), method, "/api/v1/runs/"+runCardsRunID+"/"+path, "", "")
		if rec.Code != 401 {
			t.Fatal(path, rec.Code)
		}
	}
	pool := runCardsMock(t)
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(runCardsRunID).WillReturnRows(runCardsGetRunRow(runCardsRunID, ""))
	pool.ExpectQuery("SELECT payload FROM skill_model_revisions").WithArgs(runCardsRunID, 0).WillReturnRows(pgxmock.NewRows([]string{"payload"}))
	rec := do(t, NewServer(NewPGStore(pool)).Routes(), http.MethodGet, "/api/v1/runs/"+runCardsRunID+"/skill-model", "user", "")
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var model domain.SkillModel
	if json.Unmarshal(rec.Body.Bytes(), &model) != nil || model.Revision != 0 || len(model.Tools) == 0 {
		t.Fatal(rec.Body.String())
	}
}
func TestSkillHTTPConcurrentUpdate(t *testing.T) {
	pool := runCardsMock(t)
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(runCardsRunID).WillReturnRows(runCardsGetRunRow(runCardsRunID, ""))
	pool.ExpectBegin()
	pool.ExpectQuery("SELECT id::text FROM runs").WithArgs(runCardsRunID).WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(runCardsRunID))
	old := domain.EmptySkillModel()
	old.Revision = 2
	raw, _ := json.Marshal(old)
	pool.ExpectQuery("SELECT payload FROM skill_model_revisions").WithArgs(runCardsRunID, 0).WillReturnRows(pgxmock.NewRows([]string{"payload"}).AddRow(raw))
	pool.ExpectRollback()
	body, _ := json.Marshal(domain.EmptySkillModel())
	rec := do(t, NewServer(NewPGStore(pool)).Routes(), http.MethodPut, "/api/v1/runs/"+runCardsRunID+"/skill-model", "user", string(body))
	if rec.Code != 409 {
		t.Fatal(rec.Code, rec.Body.String())
	}
}
func TestSkillHTTPSaveAndForeignEvidence(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "save", true: "foreign evidence"}[foreign], func(t *testing.T) {
			pool := runCardsMock(t)
			pool.ExpectQuery("FROM runs WHERE id=").WithArgs(runCardsRunID).WillReturnRows(runCardsGetRunRow(runCardsRunID, ""))
			pool.ExpectBegin()
			pool.ExpectQuery("SELECT id::text FROM runs").WithArgs(runCardsRunID).WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(runCardsRunID))
			pool.ExpectQuery("SELECT payload FROM skill_model_revisions").WithArgs(runCardsRunID, 0).WillReturnRows(pgxmock.NewRows([]string{"payload"}))
			nodes := []domain.Node{{ID: "n", Type: domain.NodeTask, Title: "Сохранение", Evidence: []domain.Evidence{{ID: "e", Quote: "Исходная — цитата", SourceID: "s", Status: domain.EvidencePending}}}}
			nodeJSON, _ := json.Marshal(nodes)
			pool.ExpectQuery("SELECT COALESCE.*FROM nodes n").WithArgs(runCardsRunID).WillReturnRows(pgxmock.NewRows([]string{"nodes"}).AddRow(nodeJSON))
			model := domain.EmptySkillModel()
			model.Definitions = []domain.SkillDefinition{{ID: "d", Version: 1, Title: "Транзакции", Kind: "knowledge", Tools: []domain.SkillToolUse{}}}
			evidenceID := "e"
			if foreign {
				evidenceID = "foreign"
			}
			model.Links = []domain.SkillNodeLink{{ID: "l", NodeID: "n", SkillRef: domain.SkillRef{ID: "d", Version: 1}, EvidenceIDs: []string{evidenceID}, Evidence: []domain.Evidence{{ID: "fake", Quote: "Подмена"}}, Basis: "requirement", Reason: "Сохранение заказа", Status: "proposed"}}
			if foreign {
				pool.ExpectRollback()
			} else {
				pool.ExpectExec("INSERT INTO skill_model_revisions").WithArgs(runCardsRunID, 1, pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
				pool.ExpectCommit()
			}
			body, _ := json.Marshal(model)
			rec := do(t, NewServer(NewPGStore(pool)).Routes(), http.MethodPut, "/api/v1/runs/"+runCardsRunID+"/skill-model", "user", string(body))
			if foreign {
				if rec.Code != 422 {
					t.Fatal(rec.Code, rec.Body.String())
				}
				return
			}
			if rec.Code != 200 {
				t.Fatal(rec.Code, rec.Body.String())
			}
			var got domain.SkillModel
			if json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Links[0].Evidence[0].Quote != "Исходная — цитата" || got.Revision != 1 {
				t.Fatal(rec.Body.String())
			}
		})
	}
}
func TestSkillHTTPRejectsOtherOwner(t *testing.T) {
	pool := runCardsMock(t)
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(runCardsRunID).WillReturnRows(runCardsGetRunRow(runCardsRunID, "other-user"))
	rec := do(t, NewServer(NewPGStore(pool)).Routes(), http.MethodGet, "/api/v1/runs/"+runCardsRunID+"/skill-model", "user", "")
	if rec.Code != 403 {
		t.Fatal(rec.Code, rec.Body.String())
	}
}
