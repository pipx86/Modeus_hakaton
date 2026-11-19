package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/api"
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"

	"github.com/pashagolub/pgxmock/v4"
)

func TestPostgresSynthesisReviewReplacesAndReadsEntries(t *testing.T) {
	pool, pg := testPostgresAPI(t)
	runID := pgUUID(t, dbRun)
	conflict := domain.Conflict{ID: "conflict-1", NodeID: dbNode, Field: "title"}
	message := agent.AgentMessage{ID: "message-1"}
	conflictJSON, err := json.Marshal(conflict)
	if err != nil {
		t.Fatal(err)
	}
	messageJSON, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	for _, rollback := range []bool{false, true} {
		pool.ExpectBegin()
		pool.ExpectQuery("SELECT EXISTS").WithArgs(runID).WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
		pool.ExpectExec("DELETE FROM agent_messages").WithArgs(runID, "synthesis.conflict", "synthesis.message").
			WillReturnResult(pgxmock.NewResult("DELETE", 2))
		pool.ExpectExec("INSERT INTO agent_messages").WithArgs(runID, "synthesis.conflict", string(conflictJSON),
			"sent", "synthesis.conflict:000000").WillReturnResult(pgxmock.NewResult("INSERT", 1))
		insert := pool.ExpectExec("INSERT INTO agent_messages").WithArgs(runID, "synthesis.message", string(messageJSON),
			"sent", "synthesis.message:000000")
		if rollback {
			insert.WillReturnError(errTestDB)
			pool.ExpectRollback()
		} else {
			insert.WillReturnResult(pgxmock.NewResult("INSERT", 1))
			pool.ExpectCommit()
		}
		err := pg.SaveSynthesisReview(context.Background(), dbRun, []domain.Conflict{conflict}, []agent.AgentMessage{message})
		if (rollback && !errors.Is(err, errTestDB)) || (!rollback && err != nil) {
			t.Fatalf("атомарная замена результатов: %v", err)
		}
	}
	authToken := testToken(t, auth.RoleTeacher)
	h := api.NewServer(pg).Routes()
	path := "/api/v1/runs/" + dbRun
	expectRead := func(kind string, payload []byte) {
		pool.ExpectQuery("FROM runs WHERE id=").WithArgs(dbRun).WillReturnRows(runRow())
		pool.ExpectQuery("FROM agent_messages").WithArgs(runID, kind).
			WillReturnRows(pgxmock.NewRows([]string{"payload"}).AddRow(payload))
	}
	expectRead("synthesis.conflict", conflictJSON)
	resp := callAPI(h, http.MethodGet, path+"/conflicts", authToken, nil)
	var list []domain.Conflict
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil || resp.Code != 200 || len(list) != 1 ||
		list[0].ID != conflict.ID || list[0].NodeID != dbNode {
		t.Fatalf("конфликты: %d %s, %v", resp.Code, resp.Body.String(), err)
	}
	expectRead("synthesis.message", messageJSON)
	resp = callAPI(h, http.MethodGet, path+"/agent-messages", authToken, nil)
	var messages []agent.AgentMessage
	if err := json.Unmarshal(resp.Body.Bytes(), &messages); err != nil || resp.Code != 200 || len(messages) != 1 || messages[0].ID != message.ID {
		t.Fatalf("сообщения: %d %s, %v", resp.Code, resp.Body.String(), err)
	}
	expectRead("synthesis.conflict", []byte("["))
	if resp = callAPI(h, http.MethodGet, path+"/conflicts", authToken, nil); resp.Code != 500 {
		t.Fatalf("повреждённая запись: %d %s", resp.Code, resp.Body.String())
	}
}
