package api_test

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
)

type treeExportStore struct {
	api.Store
	status domain.RunStatus
	id     string
	nodes  []domain.Node
}

func (s treeExportStore) GetRun(id string) (*domain.Run, error) {
	want := s.id
	if want == "" {
		want = "export-id"
	}
	if id != want {
		return nil, api.ErrNotFound
	}
	return &domain.Run{ID: id, Status: s.status}, nil
}

func (s treeExportStore) GetTree(string) ([]domain.Node, error) { return s.nodes, nil }

func TestRunExportFormatsOnHTTP(t *testing.T) {
	token := testToken(t, auth.RoleStudent)
	s := treeExportStore{Store: api.NewMemoryStore(), status: domain.RunDone, nodes: []domain.Node{
		{ID: "r", Type: domain.NodeType("role"), Title: "  =2026", Evidence: []domain.Evidence{
			{ID: "e1", Quote: exactAnswer, SourceID: "source"},
			{ID: "e2", Quote: "Другой фрагмент", SourceID: "source"},
		}},
		{ID: "f", Type: domain.NodeType("function"), Title: "Функция без цитат"},
	}}
	h := api.NewServer(s).Routes()
	base := "/api/v1/runs/export-id/export"
	download := callAPI(h, http.MethodGet, base+"?format=csv", token, nil)
	if download.Code != 200 || !strings.HasPrefix(download.Header().Get("Content-Type"), "text/csv") ||
		!strings.HasPrefix(download.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("csv: %d %v", download.Code, download.Header())
	}
	rows, err := csv.NewReader(download.Body).ReadAll()
	if err != nil || len(rows) != 4 || rows[1][3] != "'  =2026" || rows[1][8] != exactAnswer ||
		rows[2][3] != rows[1][3] || rows[2][5] != "e2" || rows[3][5] != "" {
		t.Fatalf("csv: %q %v", rows, err)
	}
	var legacy []domain.Node
	reply := callAPI(h, http.MethodGet, base, token, nil)
	if err := json.Unmarshal(reply.Body.Bytes(), &legacy); reply.Code != 200 || err != nil ||
		len(legacy) != 2 || legacy[0].Title != s.nodes[0].Title || legacy[0].Evidence[0].Quote != exactAnswer {
		t.Fatalf("json: %d %+v %v", reply.Code, legacy, err)
	}
	markdown := callAPI(h, http.MethodGet, base+"?format=md", token, nil)
	if markdown.Code != http.StatusOK || strings.Contains(markdown.Body.String(), "<mark") ||
		!strings.Contains(markdown.Body.String(), "&lt;mark data-proof='literal'&gt;&amp; 3 &lt; 7") {
		t.Fatalf("Markdown: %d %s", markdown.Code, markdown.Body.String())
	}
	var structured struct {
		Schema   string            `json:"schema"`
		Evidence []domain.Evidence `json:"evidence"`
	}
	structuredReply := callAPI(h, http.MethodGet, base+"?format=json", token, nil)
	if err := json.Unmarshal(structuredReply.Body.Bytes(), &structured); structuredReply.Code != http.StatusOK ||
		err != nil || structured.Schema != "run-export/v1" || len(structured.Evidence) != 2 ||
		structured.Evidence[0].Quote != exactAnswer {
		t.Fatalf("структурированный JSON: %d %s", structuredReply.Code, structuredReply.Body.String())
	}
	if invalid := callAPI(h, http.MethodGet, base+"?format=unknown", token, nil); invalid.Code != 400 {
		t.Fatalf("неизвестный формат: %d", invalid.Code)
	}
}

func TestFileImportHTTPRequiresAdminAndValidBody(t *testing.T) {
	admin := testToken(t, auth.RoleAdmin)
	teacher := testToken(t, auth.RoleTeacher)
	memory := api.NewServer(api.NewMemoryStore()).Routes()
	pgWithoutFiles := api.NewServer(api.NewPGStore(nil)).Routes()
	for _, tc := range []struct {
		name    string
		handler http.Handler
		token   string
		body    string
		status  int
	}{
		{"без JWT", memory, "", `{"sources":["vacancies"]}`, 401},
		{"роль преподавателя", pgWithoutFiles, teacher, `{"sources":["vacancies"]}`, 403},
		{"демонстрационный сервер", memory, admin, `{"sources":["vacancies"]}`, 503},
		{"файлы не настроены", pgWithoutFiles, admin, `{"sources":["standards"]}`, 503},
		{"два JSON-объекта", memory, admin, `{"sources":["vacancies"]}{}`, 400},
		{"неизвестный источник", pgWithoutFiles, admin, `{"sources":["unknown"]}`, 400},
		{"повтор источника", pgWithoutFiles, admin, `{"sources":["vacancies","vacancies"]}`, 400},
		{"нет источников", pgWithoutFiles, admin, `{"sources":[]}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := callAPI(tc.handler, http.MethodPost, "/api/v1/ingest", tc.token, []byte(tc.body))
			if resp.Code != tc.status {
				t.Fatalf("%d (%s), ожидался %d", resp.Code, resp.Body.String(), tc.status)
			}
		})
	}
}

func TestSearchHTTPRejectsInvalidParametersBeforeQuery(t *testing.T) {
	teacher := testToken(t, auth.RoleTeacher)
	student := testToken(t, auth.RoleStudent)
	const uuid = "e198b30a-8cbd-4eb9-a4a6-893238c7f954"
	s := treeExportStore{Store: api.NewMemoryStore(), id: uuid}
	handler := api.NewServer(s).Routes()
	base := "/api/v1/runs/"
	for _, tc := range []struct {
		path, token string
		code        int
	}{
		{uuid + "/search?q=abc", "", 401},
		{uuid + "/search?q=abc", student, 501},
		{"other/search?q=abc", teacher, 404},
		{uuid + "/search?q=abc&limit=text", teacher, 400},
		{uuid + "/search?q=abc&limit=0", teacher, 400},
		{uuid + "/search?q=abc&limit=101", teacher, 400},
		{uuid + "/search?q=abc&limit=1", teacher, 501},
	} {
		resp := callAPI(handler, http.MethodGet, base+tc.path, tc.token, nil)
		if resp.Code != tc.code {
			t.Fatalf("%s: %d, ожидался %d", tc.path, resp.Code, tc.code)
		}
	}
	badID := api.NewServer(treeExportStore{Store: api.NewMemoryStore(), id: "bad"}).Routes()
	if resp := callAPI(badID, http.MethodGet, base+"bad/search?q=abc", teacher, nil); resp.Code != 400 {
		t.Fatalf("поиск по неверному ID: %d", resp.Code)
	}
}
