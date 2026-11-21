package analyst

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

// fakeClient replays a fixed list of answers without any network access.
type fakeClient struct {
	t         *testing.T
	responses []string
	calls     int
}

func (c *fakeClient) Chat(ctx context.Context, messages []llm.Message) (string, error) {
	if ctx == nil {
		c.t.Error("chat called with nil context")
	}
	if len(messages) < 2 {
		c.t.Fatalf("want system+user messages, got %d", len(messages))
	}
	if !strings.Contains(messages[0].Content, "Аналитик") {
		c.t.Fatal("system prompt is not the analyst prompt")
	}
	joined := ""
	for _, message := range messages {
		joined += message.Content
	}
	if !strings.Contains(joined, "transcript") {
		c.t.Fatal("prompt history does not contain the transcript")
	}
	c.calls++
	if c.calls > len(c.responses) {
		return "", errors.New("script exhausted")
	}
	return c.responses[c.calls-1], nil
}

// demoEntities is the canned model answer for testdata/demo_transcript.txt.
const demoEntities = `{"entities":[
 {"id":"a1","type":"task","title":"Писать SQL-запросы","confidence":0.9,
  "evidence":[{"id":"e1","source_id":"src-interview-1","ref_id":"msg-4","quote":"Каждый день пишу SQL-запросы для отчётов по продажам"}]},
 {"id":"a2","type":"task","title":"  писать  SQL-запросы. ","confidence":0.8,
  "evidence":[{"id":"e2","source_id":"src-interview-1","ref_id":"msg-4","quote":"Каждый день пишу SQL-запросы"}]},
 {"id":"a3","type":"task","title":"Строить дашборды","confidence":0.85,
  "evidence":[{"id":"e3","source_id":"src-interview-1","ref_id":"msg-4","quote":"потом свожу их в дашборд"}]},
 {"id":"a4","type":"action","title":"Использовать PostgreSQL и DBeaver","parent_id":"tax-task-sql",
  "evidence":[{"id":"e4","source_id":"src-interview-1","ref_id":"msg-6","quote":"Использую PostgreSQL и DBeaver"}]},
 {"id":"a5","type":"task","title":"Собирать требования","confidence":0.7,
  "evidence":[{"id":"e5","source_id":"src-interview-1","ref_id":"msg-8","quote":"собираю требования к отчётам"}]}
]}`

func readDemoTranscript(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/demo_transcript.txt")
	if err != nil {
		t.Fatalf("read demo transcript: %v", err)
	}
	return string(data)
}

func demoInput(t *testing.T) agent.AnalystInput {
	return agent.AnalystInput{
		SessionID:  "sess-1",
		Profession: "Аналитик данных",
		Transcript: readDemoTranscript(t),
	}
}

func findNode(entities []agent.ExtractedEntity, id string) (domain.Node, bool) {
	for _, entity := range entities {
		if entity.ID == id {
			return entity.Node, true
		}
	}
	return domain.Node{}, false
}

func TestAnalyzeDemoTranscript(t *testing.T) {
	client := &fakeClient{t: t, responses: []string{demoEntities}}
	analyst, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := analyst.Analyze(context.Background(), demoInput(t))
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(out.Entities) == 0 {
		t.Fatal("no entities extracted")
	}
	transcript := readDemoTranscript(t)
	for _, entity := range out.Entities {
		if err := entity.Validate(); err != nil {
			t.Fatalf("entity %q is invalid: %v", entity.ID, err)
		}
		for _, evidence := range entity.Evidence {
			if !strings.Contains(transcript, evidence.Quote) && !strings.Contains(
				strings.Join(strings.Fields(transcript), " "),
				strings.Join(strings.Fields(evidence.Quote), " "),
			) {
				t.Fatalf("entity %q quote is not in transcript: %q", entity.ID, evidence.Quote)
			}
		}
	}
	if err := agent.ValidateOutput(agent.KindAnalyst, mustMarshal(t, out)); err != nil {
		t.Fatalf("analyze output violates contract: %v", err)
	}

	sql, ok := findNode(out.Entities, "tax-task-sql")
	if !ok {
		t.Fatalf("SQL task was not linked to taxonomy: %+v", out.Entities)
	}
	if sql.Type != domain.NodeTask {
		t.Fatalf("linked SQL node type = %q, want task", sql.Type)
	}
	if sql.ParentID == nil || *sql.ParentID != "tax-fn-reporting" {
		t.Fatalf("linked SQL node parent = %v, want tax-fn-reporting", sql.ParentID)
	}
	if len(sql.Evidence) < 2 {
		t.Fatalf("deduplicated SQL task keeps %d evidence, want >= 2", len(sql.Evidence))
	}
	if _, ok := findNode(out.Entities, "tax-task-dashboards"); !ok {
		t.Fatal("dashboards task was not linked to taxonomy")
	}
	if _, ok := findNode(out.Entities, "tax-fn-reporting"); !ok {
		t.Fatal("taxonomy function ancestor was not added")
	}
	role, ok := findNode(out.Entities, "tax-role-data-analyst")
	if !ok {
		t.Fatal("taxonomy role ancestor was not added")
	}
	if len(role.Evidence) == 0 {
		t.Fatal("generated role ancestor has no evidence")
	}
}

func TestExtractRepairsQuoteMismatch(t *testing.T) {
	bogus := `{"entities":[{"id":"a1","type":"task","title":"Писать SQL-запросы",
  "evidence":[{"id":"e1","source_id":"src-1","ref_id":"msg-1","quote":"Я готовлю кофе"}]}]}`
	client := &fakeClient{t: t, responses: []string{bogus, demoEntities}}
	analyst, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := analyst.Extract(context.Background(), demoInput(t))
	if err != nil {
		t.Fatalf("Extract after repair: %v", err)
	}
	if len(out.Entities) == 0 {
		t.Fatal("repaired extraction returned no entities")
	}
	if client.calls != 2 {
		t.Fatalf("model calls = %d, want 2 (invalid quote + repaired answer)", client.calls)
	}
}

func TestExtractSalvagesValidEntitiesAfterQuoteMismatch(t *testing.T) {
	bogus := `{"entities":[{"id":"a1","type":"task","title":"Писать SQL-запросы",
  "evidence":[{"id":"e1","source_id":"src-1","ref_id":"msg-1","quote":"этой фразы нет в транскрипте"}]}]}`
	client := &fakeClient{t: t, responses: []string{bogus, bogus, demoEntities}}
	analyst, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := analyst.Extract(context.Background(), demoInput(t))
	if err != nil {
		t.Fatalf("salvaged extraction: %v", err)
	}
	if len(out.Entities) == 0 {
		t.Fatal("salvaged extraction returned no entities")
	}
}

func TestExtractFailsAfterRepairs(t *testing.T) {
	bogus := `{"entities":[{"id":"a1","type":"task","title":"Писать SQL-запросы",
  "evidence":[{"id":"e1","source_id":"src-1","ref_id":"msg-1","quote":"этой фразы нет в транскрипте"}]}]}`
	client := &fakeClient{t: t, responses: []string{bogus, bogus, bogus}}
	analyst, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := analyst.Extract(context.Background(), demoInput(t)); !errors.Is(err, ErrModel) {
		t.Fatalf("Extract error = %v, want ErrModel", err)
	}
	if client.calls != 3 {
		t.Fatalf("model calls = %d, want 3 (initial + repair + salvage)", client.calls)
	}
}

func TestDedupMergesEvidence(t *testing.T) {
	entities := []agent.ExtractedEntity{
		{Node: domain.Node{ID: "a1", Type: domain.NodeTask, Title: "Писать SQL-запросы",
			Evidence: []domain.Evidence{{ID: "e1", SourceID: "s", RefID: "r1", Quote: "q1"}}}},
		{Node: domain.Node{ID: "a2", Type: domain.NodeTask, Title: " писать sql-запросы. ",
			Evidence: []domain.Evidence{
				{ID: "e2", SourceID: "s", RefID: "r2", Quote: "q2"},
				{ID: "e1", SourceID: "s", RefID: "r1", Quote: "q1"},
			}}},
		{Node: domain.Node{ID: "a3", Type: domain.NodeAction, Title: "Писать SQL-запросы",
			Evidence: []domain.Evidence{{ID: "e3", SourceID: "s", RefID: "r3", Quote: "q3"}}}},
	}
	deduped := DedupEntities(entities)
	if len(deduped) != 2 {
		t.Fatalf("deduped entities = %d, want 2", len(deduped))
	}
	if deduped[0].ID != "a1" {
		t.Fatalf("kept entity id = %q, want a1", deduped[0].ID)
	}
	if len(deduped[0].Evidence) != 2 {
		t.Fatalf("merged evidence = %d, want 2 (duplicate dropped)", len(deduped[0].Evidence))
	}
}

func TestDedupMergesRussianInflectionsWithoutChangingTitle(t *testing.T) {
	parentID := "function-1"
	entities := []agent.ExtractedEntity{
		{Node: domain.Node{ID: "a1", Type: domain.NodeTask, ParentID: &parentID, Title: "Настройка серверов"}},
		{Node: domain.Node{ID: "a2", Type: domain.NodeTask, ParentID: &parentID, Title: "Настройки сервера"}},
	}

	deduped := DedupEntities(entities)
	if len(deduped) != 1 {
		t.Fatalf("deduped entities = %d, want 1", len(deduped))
	}
	if deduped[0].Title != "Настройка серверов" {
		t.Fatalf("title = %q, want original title", deduped[0].Title)
	}
}

func TestNormalizeEntitiesFillsIDs(t *testing.T) {
	entities := NormalizeEntities([]agent.ExtractedEntity{{
		Node: domain.Node{
			Type:  domain.NodeTask,
			Title: "  Писать SQL-запросы ",
			Evidence: []domain.Evidence{
				{SourceID: "s", RefID: "r", Quote: "  цитата  "},
			},
		},
	}})
	if entities[0].ID == "" {
		t.Fatal("entity id was not filled")
	}
	if entities[0].Title != "Писать SQL-запросы" {
		t.Fatalf("title = %q", entities[0].Title)
	}
	if entities[0].Evidence[0].ID == "" {
		t.Fatal("evidence id was not filled")
	}
	if entities[0].Evidence[0].Quote != "цитата" {
		t.Fatalf("quote = %q", entities[0].Evidence[0].Quote)
	}
}

func TestDefaultTaxonomyValidAndLookup(t *testing.T) {
	taxonomy, err := DefaultTaxonomy()
	if err != nil {
		t.Fatalf("DefaultTaxonomy: %v", err)
	}
	if taxonomy.Version == "" {
		t.Fatal("taxonomy version is empty")
	}
	for _, alias := range []string{"SQL", "sql-запросы", "Писать SQL-запросы"} {
		entry, ok := taxonomy.Lookup(alias)
		if !ok || entry.ID != "tax-task-sql" {
			t.Fatalf("Lookup(%q) = %+v, %v; want tax-task-sql", alias, entry, ok)
		}
	}
	if entry, ok := taxonomy.LookupID("tax-fn-reporting"); !ok || entry.Type != domain.NodeFunction {
		t.Fatalf("LookupID(tax-fn-reporting) = %+v, %v", entry, ok)
	}
}

func TestTaxonomyValidation(t *testing.T) {
	bad := `{"version":"x","entries":[{"id":"t","type":"task","title":"Задача","parent_id":"missing"}]}`
	if _, err := LoadTaxonomy([]byte(bad)); !errors.Is(err, ErrTaxonomyInvalid) {
		t.Fatalf("LoadTaxonomy error = %v, want ErrTaxonomyInvalid", err)
	}
	badHierarchy := `{"version":"x","entries":[
	 {"id":"r","type":"role","title":"Роль"},
	 {"id":"t","type":"task","title":"Задача","parent_id":"r"}]}`
	if _, err := LoadTaxonomy([]byte(badHierarchy)); !errors.Is(err, ErrTaxonomyInvalid) {
		t.Fatalf("invalid hierarchy error = %v, want ErrTaxonomyInvalid", err)
	}
	duplicate := `{"version":"x","entries":[
	 {"id":"r","type":"role","title":"Роль"},
	 {"id":"r","type":"role","title":"Роль 2"}]}`
	if _, err := LoadTaxonomy([]byte(duplicate)); !errors.Is(err, ErrTaxonomyInvalid) {
		t.Fatalf("duplicate id error = %v, want ErrTaxonomyInvalid", err)
	}
}

func TestTaxonomyLinkAddsAncestorsWithEvidence(t *testing.T) {
	taxonomy, err := DefaultTaxonomy()
	if err != nil {
		t.Fatalf("DefaultTaxonomy: %v", err)
	}
	evidence := domain.Evidence{ID: "e1", SourceID: "src-1", RefID: "msg-1", Quote: "каждый день пишу sql-запросы"}
	linked := taxonomy.Link([]domain.Node{{
		ID: "raw-1", Type: domain.NodeTask, Title: "Писать SQL-запросы",
		Evidence: []domain.Evidence{evidence},
	}})
	if len(linked) != 3 {
		t.Fatalf("linked nodes = %d, want role+function+task", len(linked))
	}
	for _, node := range linked {
		if err := node.Validate(); err != nil {
			t.Fatalf("linked node %q invalid: %v", node.ID, err)
		}
	}
	task, ok := findNode(NodesToEntities(linked), "tax-task-sql")
	if !ok || task.ParentID == nil || *task.ParentID != "tax-fn-reporting" {
		t.Fatalf("linked task = %+v, %v", task, ok)
	}
	function, ok := findNode(NodesToEntities(linked), "tax-fn-reporting")
	if !ok || function.ParentID == nil || *function.ParentID != "tax-role-data-analyst" {
		t.Fatalf("linked function = %+v, %v", function, ok)
	}
	role, ok := findNode(NodesToEntities(linked), "tax-role-data-analyst")
	if !ok || len(role.Evidence) == 0 {
		t.Fatalf("linked role = %+v, %v", role, ok)
	}
}

func TestNewRequiresClient(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) accepted, want error")
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
