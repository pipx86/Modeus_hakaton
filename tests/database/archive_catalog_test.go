package database_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/ingestion"

	"github.com/jackc/pgx/v5/pgxpool"
)

func archivePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("STORE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная STORE_TEST_DATABASE_URL")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	auth.SetSecretForTests("archive-catalog-local-fixture")
	return pool
}

func archiveRequest(t *testing.T, h http.Handler, role auth.Role, method, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if role != "" {
		token, err := auth.Issue("catalog-fixture", role, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("X-Run-ID", key)
	}
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	return response
}

func archiveGet(t *testing.T, h http.Handler, path string, destination any) {
	t.Helper()
	response := archiveRequest(t, h, auth.RoleUser, http.MethodGet, path, "", "")
	if err := json.Unmarshal(response.Body.Bytes(), destination); response.Code != http.StatusOK || err != nil {
		t.Fatalf("%s: %d %s (%v)", path, response.Code, response.Body.String(), err)
	}
}

func TestArchiveVacancyCatalogImportsAndFindsMarketFields(t *testing.T) {
	t.Setenv("MODEUS_ENABLE_DEFERRED_API", "true")
	pool := archivePool(t)
	ctx := context.Background()
	suffix := fmt.Sprint(time.Now().UnixNano())
	id, oldID, other := "catalog-"+suffix, "legacy-"+suffix, "other-"+suffix
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM sources WHERE provider='hh' AND external_id=ANY($1::text[])", []string{id, oldID, other})
	})
	title := "Каталог % " + suffix
	content := fmt.Sprintf(`[
        {"id":%q,"name":%q,"description":"Сохранённый текст","published_at":"2026-11-18T00:10:00+03:00","area":{"name":"Город теста"},
         "employer":{"id":"employer","name":"Работодатель теста"},"experience":{"id":"noExperience"},
         "salary":{"from":100,"to":150,"currency":"XTS"},"key_skills":[{"name":"Go"},"go"," PostgreSQL "," "]},
        {"id":%q,"name":%q,"published_at":"2026-11-19T00:00:00+03:00","area":"Город теста","experience":"senior"},
        {"id":%q,"name":"Вакансия без рыночных полей"}
    ]`, id, title, other, title+" другая", oldID)
	rows, err := ingestion.ReadVacancies(strings.NewReader(content), "json")
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []ingestion.Counts{{Inserted: 3}, {Skipped: 3}} {
		got, err := ingestion.SaveVacancies(ctx, pool, rows)
		if err != nil || got != want {
			t.Fatalf("импорт %d: %+v, %v", i, got, err)
		}
	}
	h := api.NewServer(api.NewPGStore(pool)).Routes()
	path := "/api/v1/vacancies"
	query := url.Values{"search": {title}, "region": {"Город теста"}, "date_from": {"2026-11-18"}, "date_to": {"2026-11-18"}, "limit": {"1"}}
	type vacancy struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Area        string   `json:"area"`
		Experience  string   `json:"experience"`
		Employer    string   `json:"employer_name"`
		Description string   `json:"description"`
		Skills      []string `json:"key_skills"`
	}
	var page struct {
		Items   []vacancy
		Total   int
		HasMore bool `json:"has_more"`
	}
	archiveGet(t, h, path+"?"+query.Encode(), &page)
	if page.Total != 1 || page.HasMore || len(page.Items) != 1 || page.Items[0].ID != id ||
		page.Items[0].Experience != "junior" || page.Items[0].Employer != "Работодатель теста" ||
		strings.Join(page.Items[0].Skills, ",") != "Go,PostgreSQL" || page.Items[0].Description != "" {
		t.Fatalf("фильтры и список: %+v", page)
	}
	query.Set("offset", "1")
	archiveGet(t, h, path+"?"+query.Encode(), &page)
	if page.Total != 1 || len(page.Items) != 0 {
		t.Fatalf("страница за диапазоном: %+v", page)
	}
	var detail vacancy
	archiveGet(t, h, path+"/"+id, &detail)
	if detail.Description != "Сохранённый текст" {
		t.Fatalf("детали: %+v", detail)
	}
	var legacy vacancy
	archiveGet(t, h, path+"/"+oldID, &legacy)
	if legacy.ID != oldID || len(legacy.Skills) != 0 {
		t.Fatalf("старая запись: %+v", legacy)
	}
	var info struct {
		WithSkills int `json:"with_skills"`
	}
	archiveGet(t, h, path+"/info", &info)
	if info.WithSkills < 1 {
		t.Fatalf("нет навыков в метаданных: %+v", info)
	}
	var stats struct {
		SalaryByCurrency []struct {
			Currency string
			Average  int
		} `json:"salary_by_currency"`
	}
	archiveGet(t, h, path+"/stats/summary", &stats)
	foundSalary := false
	for _, salary := range stats.SalaryByCurrency {
		foundSalary = foundSalary || (salary.Currency == "XTS" && salary.Average == 100)
	}
	if !foundSalary {
		t.Fatalf("выборка зарплат: %+v", stats)
	}
	var rankings struct {
		Skills []struct{ Name string } `json:"top_skills"`
	}
	archiveGet(t, h, path+"/stats/analytics", &rankings)
	if len(rankings.Skills) == 0 {
		t.Fatal("отсутствует рейтинг навыков")
	}
	csv := "id,name,description,area,experience\n" + id + "," + title + ",Сохранённый текст,Новый город,noExperience\n"
	rows, err = ingestion.ReadVacancies(strings.NewReader(csv), "csv")
	if err != nil {
		t.Fatal(err)
	}
	counts, err := ingestion.SaveVacancies(ctx, pool, rows)
	if err != nil || counts != (ingestion.Counts{Updated: 1}) {
		t.Fatalf("отдельное обновление рыночных полей: %+v, %v", counts, err)
	}
	archiveGet(t, h, path+"/"+id, &detail)
	if detail.Area != "Новый город" || len(detail.Skills) != 2 {
		t.Fatalf("обновление без потери известных полей: %+v", detail)
	}
}

func TestArchiveAcademicReadsImportedProgramsAndReportsGap(t *testing.T) {
	pool := archivePool(t)
	ctx := context.Background()
	key := fmt.Sprintf("academic-%d", time.Now().UnixNano())
	taxonomy, err := ingestion.SaveTaxonomy(ctx, pool, key, ingestion.Taxonomy{Name: key, Items: []ingestion.TaxonomyItem{
		{Code: "go", Title: "Go"}, {Code: "sql", Title: "SQL", ParentCode: "go"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	report, err := ingestion.SaveEducation(ctx, pool, key, taxonomy, ingestion.EducationProgram{
		Title: key, Code: "09.02.00", Competencies: []ingestion.ProgramCompetency{
			{Code: "ПК1", Title: "Разработка", TaxonomyCodes: []string{"go"}, KSA: []ingestion.ProgramKSA{{Kind: "skill", Title: "Тестировать", TaxonomyCodes: []string{"unknown"}}}},
			{Code: "ПК2", Title: "Работа с данными"},
		},
	})
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM education_programs WHERE id=$1", report.ProgramID)
		_, _ = pool.Exec(ctx, "DELETE FROM taxonomies WHERE id=$1", taxonomy)
		_, _ = pool.Exec(ctx, "DELETE FROM sources WHERE provider='file' AND external_id IN ($1,$2)", key, "education:"+key)
	})
	if err != nil {
		t.Fatal(err)
	}
	h := api.NewServer(api.NewPGStore(pool)).Routes()
	base := "/api/v1/academic/programs"
	var list struct {
		Total int
		Items []struct{ ID string }
	}
	archiveGet(t, h, base+"?search="+url.QueryEscape(key), &list)
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].ID != report.ProgramID {
		t.Fatalf("список программ: %+v", list)
	}
	var detail struct {
		ID           string
		Competencies []struct {
			Code string
			KSA  []struct{ Title string }
		}
	}
	archiveGet(t, h, base+"/"+report.ProgramID, &detail)
	if detail.ID != report.ProgramID || len(detail.Competencies) != 2 || len(detail.Competencies[0].KSA) != 1 {
		t.Fatalf("программа: %+v", detail)
	}
	type gap struct {
		Matched        int                      `json:"matched_competencies"`
		UnmatchedKSA   []struct{ Title string } `json:"unmatched_ksa"`
		UncoveredItems []struct{ Code string }  `json:"uncovered_taxonomy_items"`
	}
	verifyGap := func(g gap) {
		t.Helper()
		if g.Matched != 1 || len(g.UnmatchedKSA) != 1 || g.UnmatchedKSA[0].Title != "Тестировать" ||
			len(g.UncoveredItems) != 1 || g.UncoveredItems[0].Code != "sql" {
			t.Fatalf("результат сверки: %+v", g)
		}
	}
	var got gap
	archiveGet(t, h, base+"/"+report.ProgramID+"/summary?taxonomy_id="+taxonomy, &got)
	verifyGap(got)
	body := fmt.Sprintf(`{"program_id":%q,"taxonomy_id":%q}`, report.ProgramID, taxonomy)
	local := archiveRequest(t, h, auth.RoleAdmin, http.MethodPost, "/api/v1/academic/analyze-gap-local", "", body)
	got = gap{}
	if err := json.Unmarshal(local.Body.Bytes(), &got); local.Code != 200 || err != nil {
		t.Fatalf("локальный анализ: %d %s, %v", local.Code, local.Body.String(), err)
	}
	verifyGap(got)
	var tax struct {
		Items []struct {
			ID       string
			Code     string
			ParentID *string `json:"parent_id"`
		}
	}
	archiveGet(t, h, "/api/v1/taxonomies/"+taxonomy, &tax)
	if len(tax.Items) != 2 || tax.Items[1].ParentID == nil || *tax.Items[1].ParentID != tax.Items[0].ID {
		t.Fatalf("иерархия справочника: %+v", tax)
	}
}

func TestArchiveKeyedImportSurvivesServerRecreationAndRollsBack(t *testing.T) {
	pool := archivePool(t)
	ctx := context.Background()
	key := fmt.Sprintf("workflow-%d", time.Now().UnixNano())
	vacancyPath := filepath.Join(t.TempDir(), "vacancies.csv")
	standardsPath := filepath.Join(t.TempDir(), "standard.json")
	if err := os.WriteFile(vacancyPath, []byte("id,name\n"+key+",Инженер\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(standardsPath, []byte(`{"title":"Стандарт проверки","content":"Текст стандарта"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM sources WHERE external_id IN ($1,$2)", key, "standard:"+key)
	})
	makeServer := func() http.Handler {
		server := api.NewServer(api.NewPGStore(pool))
		server.SetImportFiles(ingestion.ImportFiles{Vacancies: vacancyPath, VacancyFormat: "csv", Standard: standardsPath, StandardKey: key})
		return server.Routes()
	}
	path := "/api/v1/ingest"
	first := archiveRequest(t, makeServer(), auth.RoleAdmin, http.MethodPost, path, key, `{"sources":["vacancies","standards"]}`)
	var report ingestion.FileImportReport
	if err := json.Unmarshal(first.Body.Bytes(), &report); first.Code != 200 || err != nil || report.Ingested != 2 {
		t.Fatalf("первый импорт: %d %s, %v", first.Code, first.Body.String(), err)
	}
	if err := os.Remove(vacancyPath); err != nil {
		t.Fatal(err)
	}
	replay := archiveRequest(t, makeServer(), auth.RoleAdmin, http.MethodPost, path, key, `{"sources":["standards","vacancies"]}`)
	if replay.Code != 200 || replay.Body.String() != first.Body.String() {
		t.Fatalf("результат после перезапуска: %d %s", replay.Code, replay.Body.String())
	}
	conflict := archiveRequest(t, makeServer(), auth.RoleAdmin, http.MethodPost, path, key, `{"sources":["standards"]}`)
	if conflict.Code != 409 {
		t.Fatalf("один ключ для другого запроса: %d %s", conflict.Code, conflict.Body.String())
	}
	ready := archiveRequest(t, makeServer(), "", http.MethodGet, "/api/v1/ready", "", "")
	if ready.Code != 200 {
		t.Fatalf("готовность тестовой PostgreSQL: %d %s", ready.Code, ready.Body.String())
	}

	// PostgreSQL отклоняет NUL в тексте стандарта уже после записи вакансии.
	deniedID := key + "-rollback"
	if err := os.WriteFile(vacancyPath, []byte("id,name\n"+deniedID+",Инженер\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(standardsPath, []byte("{"+"\"title\":\"Стандарт\",\"content\":\"Текст\\u0000\"}"), 0600); err != nil {
		t.Fatal(err)
	}
	broken := api.NewServer(api.NewPGStore(pool))
	broken.SetImportFiles(ingestion.ImportFiles{Vacancies: vacancyPath, VacancyFormat: "csv", Standard: standardsPath, StandardKey: key + "-denied"})
	failed := archiveRequest(t, broken.Routes(), auth.RoleAdmin, http.MethodPost, path, deniedID, `{"sources":["vacancies","standards"]}`)
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sources WHERE external_id=$1", deniedID).Scan(&count); failed.Code != 500 || err != nil || count != 0 {
		t.Fatalf("частичная запись импорта: %d %s, записей %d, %v", failed.Code, failed.Body.String(), count, err)
	}
}

func TestArchiveSkillHistoryImportAndForecastRoutes(t *testing.T) {
	t.Setenv("MODEUS_ENABLE_DEFERRED_API", "true")
	pool := archivePool(t)
	ctx := context.Background()
	profession := fmt.Sprintf("Профессия %d", time.Now().UnixNano())
	series := "profession_" + profession
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM skill_frequency_snapshots WHERE series=$1", series)
	})
	dir := t.TempDir()
	var paths []string
	for index, month := range []string{"2026-01", "2026-02", "2026-03", "2026-04"} {
		skillGap := ""
		if index != 1 {
			skillGap = `,"Пропуск":90`
		}
		body := fmt.Sprintf(`{"_meta":{"snapshot_date":%q,"profession":%q},"Go":%d,"SQL":%d,"Стабильный":100%s}`,
			month, profession, 200-index*30, 100+index*20, skillGap)
		path := filepath.Join(dir, "freq_profession_"+month+".json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	for _, want := range []int64{4, 0} {
		changed, err := ingestion.ImportSkillSnapshots(ctx, pool, paths)
		if err != nil || changed != want {
			t.Fatalf("снимки: %d, %v, ожидалось %d", changed, err, want)
		}
	}
	h := api.NewServer(api.NewPGStore(pool)).Routes()
	query := "?series=" + url.QueryEscape(series)
	var meta struct {
		Snapshots int64 `json:"snapshots"`
		Ready     bool  `json:"ready"`
	}
	archiveGet(t, h, "/api/v1/forecast/engine"+query, &meta)
	if !meta.Ready || meta.Snapshots != 4 {
		t.Fatalf("история серии: %+v", meta)
	}
	type prediction struct {
		Skill          string  `json:"skill"`
		ObservedChange float64 `json:"observed_change_pct"`
		ForecastMonths int     `json:"forecast_months"`
		NextFrequency  float64 `json:"next_year_frequency"`
	}
	var observed struct {
		RequiredPoints int          `json:"required_points"`
		MaxPoints      int          `json:"max_points"`
		Forecasts      []prediction `json:"forecasts"`
	}
	archiveGet(t, h, "/api/v1/forecast/observed"+query+"&direction=all", &observed)
	if observed.RequiredPoints != 4 || observed.MaxPoints != 4 || len(observed.Forecasts) != 3 ||
		observed.Forecasts[0].Skill != "Go" || observed.Forecasts[0].ObservedChange != -45 ||
		observed.Forecasts[2].Skill != "SQL" || observed.Forecasts[2].ObservedChange != 60 {
		t.Fatalf("наблюдения: %+v", observed)
	}
	archiveGet(t, h, "/api/v1/forecast/observed"+query+"&months=6", &observed)
	if len(observed.Forecasts) != 0 || observed.RequiredPoints != 7 {
		t.Fatalf("неполная история: %+v", observed)
	}
	var result prediction
	archiveGet(t, h, "/api/v1/forecast/go"+query+"&months=12", &result)
	if result.Skill != "Go" || result.ForecastMonths != 2 || result.NextFrequency >= 100 || result.NextFrequency <= 0 {
		t.Fatalf("прогноз: %+v", result)
	}
	var page struct {
		Total     int          `json:"total"`
		Forecasts []prediction `json:"forecasts"`
	}
	archiveGet(t, h, "/api/v1/forecast/top"+query+"&direction=growing", &page)
	if page.Total != 1 || len(page.Forecasts) != 1 || page.Forecasts[0].Skill != "SQL" {
		t.Fatalf("топ растущих: %+v", page)
	}
	archiveGet(t, h, "/api/v1/forecast/popular"+query+"&n=1", &page)
	if page.Total != 2 || len(page.Forecasts) != 1 || page.Forecasts[0].Skill != "SQL" {
		t.Fatalf("популярные: %+v", page)
	}
	archiveGet(t, h, "/api/v1/forecast/all"+query+"&limit=2&offset=2", &page)
	if page.Total != 4 || len(page.Forecasts) != 2 {
		t.Fatalf("пагинация: %+v", page)
	}
	response := archiveRequest(t, h, auth.RoleUser, http.MethodGet, "/api/v1/forecast/missing"+query, "", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("отсутствующий навык: %d, %s", response.Code, response.Body.String())
	}
}

func TestArchivePublicationNotificationsAreScopedAndDeduplicated(t *testing.T) {
	pool := archivePool(t)
	ctx := context.Background()
	suffix := fmt.Sprint(time.Now().UnixNano())
	owner, stranger, operator := "publisher-"+suffix, "stranger-"+suffix, "operator-"+suffix
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM publication_notifications WHERE owner_id=$1", owner)
		_, _ = pool.Exec(ctx, "DELETE FROM publication_subscriptions WHERE owner_id=$1", owner)
	})
	h := api.NewServer(api.NewPGStore(pool)).Routes()
	request := func(subject string, role auth.Role, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		token, err := auth.Issue(subject, role, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, req)
		if recorder.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, recorder.Code, recorder.Body.String())
		}
		return recorder
	}
	created := request(owner, auth.RoleUser, http.MethodPost, "/subscriptions",
		`{"topic":" ИТ и образование ","source":"openalex","email":"fixture@example.invalid"}`, http.StatusOK)
	var subscription struct{ ID string }
	if err := json.Unmarshal(created.Body.Bytes(), &subscription); err != nil || subscription.ID == "" {
		t.Fatalf("подписка: %+v, %v", subscription, err)
	}
	request(stranger, auth.RoleUser, http.MethodDelete, "/subscriptions/"+subscription.ID, "", http.StatusNotFound)
	request(owner, auth.RoleUser, http.MethodGet, "/internal/subscriptions", "", http.StatusForbidden)
	polling := request(operator, auth.RoleAdmin, http.MethodGet, "/internal/subscriptions?limit=200", "", http.StatusOK)
	if !strings.Contains(polling.Body.String(), subscription.ID) || strings.Contains(polling.Body.String(), "example.invalid") {
		t.Fatalf("данные для сборщика: %s", polling.Body.String())
	}
	payload := fmt.Sprintf(`{"subscription_id":%q,"user_id":%q,"articles":[
		{"title":"Research <&>","abstract":"Исходный текст","url":"https://example.invalid/research","source":"openalex"},
		{"title":"Preprint","url":"https://example.invalid/preprint","source":"arxiv"}]}`, subscription.ID, stranger)
	request(stranger, auth.RoleUser, http.MethodPost, "/webhooks/new-publications", payload, http.StatusForbidden)
	for _, want := range []string{`"created":1`, `"created":0`} {
		result := request(operator, auth.RoleAdmin, http.MethodPost, "/webhooks/new-publications", payload, http.StatusOK)
		if !strings.Contains(result.Body.String(), want) {
			t.Fatalf("результат callback: %s", result.Body.String())
		}
	}
	unread := request(owner, auth.RoleUser, http.MethodGet, "/notifications/unread-count", "", http.StatusOK)
	if !strings.Contains(unread.Body.String(), `"unread_count":1`) {
		t.Fatalf("счётчик: %s", unread.Body.String())
	}
	notifications := request(owner, auth.RoleUser, http.MethodGet, "/notifications?unread_only=true", "", http.StatusOK)
	var list struct {
		Notifications []struct{ ID, Title string }
	}
	if err := json.Unmarshal(notifications.Body.Bytes(), &list); err != nil || len(list.Notifications) != 1 ||
		list.Notifications[0].Title != "Research <&>" {
		t.Fatalf("уведомления владельца: %+v, %v", list, err)
	}
	mark := "/notifications/" + list.Notifications[0].ID + "/read"
	request(stranger, auth.RoleUser, http.MethodPost, mark, "", http.StatusNotFound)
	request(owner, auth.RoleUser, http.MethodPost, mark, "", http.StatusOK)
	unread = request(owner, auth.RoleUser, http.MethodGet, "/notifications/unread-count", "", http.StatusOK)
	if !strings.Contains(unread.Body.String(), `"unread_count":0`) {
		t.Fatalf("счётчик после чтения: %s", unread.Body.String())
	}
	request(owner, auth.RoleUser, http.MethodDelete, "/subscriptions/"+subscription.ID, "", http.StatusOK)
	result := request(owner, auth.RoleUser, http.MethodGet, "/notifications", "", http.StatusOK)
	if !strings.Contains(result.Body.String(), `"subscription_id":null`) {
		t.Fatalf("история сохраняется после удаления подписки: %s", result.Body.String())
	}
}
