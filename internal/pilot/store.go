package pilot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"activity-descriptor/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// sourceAuditLimit ограничивает выборку источников: аудит пилота не должен
	// вычитывать всю таблицу sources.
	sourceAuditLimit = 1000
	// analyzerLimit — лимит выборки существующего адаптера вакансий.
	analyzerLimit = 100
	// maxProfessionTokens ограничивает число условий ILIKE для длинных названий.
	maxProfessionTokens = 8
)

// Store читает данные для аудита пилота. Все методы выполняют только SELECT и
// ничего не меняют в базе.
type Store struct {
	pool      *pgxpool.Pool
	vacancies *store.VacancyAdapter
}

// New создаёт читающее хранилище аудита поверх пула PostgreSQL. Пул должен
// быть создан и проверен вызывающей стороной.
func New(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:      pool,
		vacancies: store.NewVacancyAdapter(store.New(pool)),
	}
}

const sourceAuditColumns = `id::text, type, title, COALESCE(content, ''), COALESCE(source_url, ''), created_at, vacancy_published_at`

// SourcesByProfession возвращает источники, относящиеся к профессии, через
// ILIKE по значимым словам профессии. Источники, где совпало больше слов,
// идут первыми: это тот же принцип, что использует адаптер вакансий. Точная
// фраза входит в выборку, потому что содержит все свои слова.
func (s *Store) SourcesByProfession(ctx context.Context, profession string) ([]SourceRow, error) {
	profession = strings.TrimSpace(profession)
	if profession == "" {
		return nil, errors.New("профессия не задана")
	}
	tokens := professionKeywords(profession)
	if len(tokens) == 0 {
		return []SourceRow{}, nil
	}

	conditions := make([]string, 0, len(tokens))
	scoreParts := make([]string, 0, len(tokens))
	args := make([]any, 0, len(tokens)+1)
	for _, token := range tokens {
		args = append(args, likePattern(token))
		condition := fmt.Sprintf("(title ILIKE $%d OR content ILIKE $%d)", len(args), len(args))
		conditions = append(conditions, condition)
		scoreParts = append(scoreParts, fmt.Sprintf("(CASE WHEN %s THEN 1 ELSE 0 END)", condition))
	}
	args = append(args, sourceAuditLimit)
	query := `SELECT ` + sourceAuditColumns + `
FROM sources
WHERE ` + strings.Join(conditions, " OR ") + `
ORDER BY (` + strings.Join(scoreParts, " + ") + `) DESC, created_at, id
LIMIT $` + fmt.Sprint(len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("выбрать источники по профессии: %w", err)
	}
	result, err := scanSourceRows(rows)
	if err != nil {
		return nil, fmt.Errorf("прочитать источники по профессии: %w", err)
	}
	return result, nil
}

// MatchedSourceIDs возвращает ID источников, которые нашёл существующий
// анализатор вакансий. Это тот же принцип, что использует сервер: адаптер
// смотрит вакансии hh по фразе, затем по токенам.
func (s *Store) MatchedSourceIDs(ctx context.Context, profession string) ([]string, error) {
	profession = strings.TrimSpace(profession)
	if profession == "" {
		return nil, errors.New("профессия не задана")
	}
	records, err := s.vacancies.ByProfession(ctx, profession, analyzerLimit)
	if err != nil {
		return nil, fmt.Errorf("найти источники анализатором: %w", err)
	}
	ids := make([]string, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		id := normalizeID(record.SourceID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, record.SourceID)
	}
	return ids, nil
}

// programFactsQuery считает структурированные данные программы. Признаки
// дисциплин и результатов обучения проверяются по именам таблиц: в текущей
// схеме их нет, поэтому оба признака будут false. Это именно наличие структуры,
// а не гарантия, что у конкретной программы есть строки.
const programFactsQuery = `
SELECT p.id::text,
       p.title,
       (SELECT count(*) FROM program_competencies c WHERE c.program_id = p.id)::int AS competencies,
       (SELECT count(*) FROM competency_ksa k
          JOIN program_competencies c ON c.id = k.competency_id
         WHERE c.program_id = p.id)::int AS ksa,
       ((SELECT count(*) FROM competency_taxonomy_links l
           JOIN program_competencies c ON c.id = l.competency_id
          WHERE c.program_id = p.id)
        + (SELECT count(*) FROM ksa_taxonomy_links l
             JOIN competency_ksa k ON k.id = l.ksa_id
             JOIN program_competencies c ON c.id = k.competency_id
            WHERE c.program_id = p.id))::int AS taxonomy_links,
       (to_regclass('public.learning_outcomes') IS NOT NULL
        OR to_regclass('public.program_learning_outcomes') IS NOT NULL) AS has_learning_outcomes,
       (to_regclass('public.disciplines') IS NOT NULL
        OR to_regclass('public.program_disciplines') IS NOT NULL) AS has_disciplines
FROM education_programs p
WHERE p.id = $1::uuid
`

// LoadProgramFacts читает программу и её структурные данные. Если программы с
// таким ID нет, возвращается ProgramFacts{Found: false} без ошибки: это
// проблема данных, а не недоступность базы.
func (s *Store) LoadProgramFacts(ctx context.Context, programID string) (ProgramFacts, error) {
	programID = strings.TrimSpace(programID)
	var id pgtype.UUID
	if err := id.Scan(programID); err != nil || !id.Valid {
		return ProgramFacts{}, fmt.Errorf("некорректный UUID программы: %q", programID)
	}

	facts := ProgramFacts{ID: programID}
	err := s.pool.QueryRow(ctx, programFactsQuery, id).Scan(
		&facts.ID,
		&facts.Title,
		&facts.Competencies,
		&facts.KSA,
		&facts.TaxonomyLinks,
		&facts.HasLearningOutcomes,
		&facts.HasDisciplines,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProgramFacts{ID: programID}, nil
	}
	if err != nil {
		return ProgramFacts{}, fmt.Errorf("прочитать данные программы: %w", err)
	}
	facts.Found = true
	return facts, nil
}

// scanSourceRows закрывает набор строк и преобразует nullable-дату публикации.
func scanSourceRows(rows pgx.Rows) ([]SourceRow, error) {
	defer rows.Close()
	result := make([]SourceRow, 0)
	for rows.Next() {
		var row SourceRow
		var publishedAt pgtype.Timestamptz
		if err := rows.Scan(&row.ID, &row.Type, &row.Title, &row.Content, &row.URL, &row.CreatedAt, &publishedAt); err != nil {
			return nil, err
		}
		if publishedAt.Valid {
			row.PublishedAt = publishedAt.Time
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// professionKeywords повторяет принцип store.professionTokens: нижний регистр,
// слова длиной от двух символов, без повторов. Функция намеренно локальная:
// store.professionTokens не экспортирован.
func professionKeywords(profession string) []string {
	words := strings.FieldsFunc(strings.ToLower(profession), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := make(map[string]bool, len(words))
	result := make([]string, 0, len(words))
	for _, word := range words {
		if len([]rune(word)) < 2 || seen[word] {
			continue
		}
		seen[word] = true
		result = append(result, word)
		if len(result) == maxProfessionTokens {
			break
		}
	}
	return result
}

// likeEscaper защищает ручной ввод от подстановки шаблонов ILIKE.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func likePattern(value string) string {
	return "%" + likeEscaper.Replace(value) + "%"
}
