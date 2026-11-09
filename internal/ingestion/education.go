package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ProgramKSA struct {
	Kind          string   `json:"kind"`
	Title         string   `json:"title"`
	TaxonomyCodes []string `json:"taxonomy_codes"`
}
type ProgramCompetency struct {
	Code          string       `json:"code"`
	Title         string       `json:"title"`
	TaxonomyCodes []string     `json:"taxonomy_codes"`
	KSA           []ProgramKSA `json:"ksa"`
}
type EducationProgram struct {
	Title        string              `json:"title"`
	Organization string              `json:"organization"`
	Code         string              `json:"code"`
	Competencies []ProgramCompetency `json:"competencies"`
}

// ReadEducation читает подготовленное структурированное представление программы.
func ReadEducation(r io.Reader) (EducationProgram, error) {
	var result EducationProgram
	data, err := io.ReadAll(io.LimitReader(r, 20<<20+1))
	if err != nil {
		return result, err
	}
	if len(data) > 20<<20 {
		return result, errors.New("файл больше 20 МБ")
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return result, fmt.Errorf("JSON: %w", err)
	}
	result.Title = strings.TrimSpace(result.Title)
	if result.Title == "" || len(result.Competencies) == 0 {
		return result, errors.New("нужны название программы и компетенции")
	}
	seen := map[string]bool{}
	for i := range result.Competencies {
		c := &result.Competencies[i]
		c.Code = strings.TrimSpace(c.Code)
		c.Title = strings.TrimSpace(c.Title)
		if c.Code == "" || c.Title == "" {
			return result, fmt.Errorf("компетенция %d: обязательны code и title", i+1)
		}
		if seen[c.Code] {
			return result, fmt.Errorf("повтор кода компетенции %s", c.Code)
		}
		seen[c.Code] = true
		ksaSeen := map[string]bool{}
		for j := range c.KSA {
			k := &c.KSA[j]
			k.Kind = strings.TrimSpace(k.Kind)
			k.Title = strings.TrimSpace(k.Title)
			if k.Kind != "knowledge" && k.Kind != "skill" && k.Kind != "ability" {
				return result, fmt.Errorf("неизвестный вид KSA у %s", c.Code)
			}
			if k.Title == "" {
				return result, fmt.Errorf("пустое название KSA у %s", c.Code)
			}
			id := k.Kind + "\x00" + k.Title
			if ksaSeen[id] {
				return result, fmt.Errorf("повтор KSA у %s", c.Code)
			}
			ksaSeen[id] = true
		}
	}
	return result, nil
}

// ReadEducationText читает размеченный текст, а не пытается угадывать смысл документа.
func ReadEducationText(text, title string) (EducationProgram, error) {
	program := EducationProgram{Title: strings.TrimSpace(title)}
	for n, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		label, value, found := strings.Cut(line, ":")
		if !found {
			return program, fmt.Errorf("строка %d: ожидается метка и двоеточие", n+1)
		}
		label = strings.ToLower(strings.TrimSpace(label))
		value = strings.TrimSpace(value)
		switch label {
		case "компетенция":
			code, name, ok := strings.Cut(value, "|")
			if !ok || strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" {
				return program, fmt.Errorf("строка %d: компетенция записывается как код | название", n+1)
			}
			program.Competencies = append(program.Competencies, ProgramCompetency{Code: strings.TrimSpace(code), Title: strings.TrimSpace(name)})
		case "знание", "умение", "навык":
			if len(program.Competencies) == 0 {
				return program, fmt.Errorf("строка %d: сначала укажите компетенцию", n+1)
			}
			kind := map[string]string{"знание": "knowledge", "умение": "skill", "навык": "ability"}[label]
			last := len(program.Competencies) - 1
			program.Competencies[last].KSA = append(program.Competencies[last].KSA, ProgramKSA{Kind: kind, Title: value})
		default:
			return program, fmt.Errorf("строка %d: неизвестная метка", n+1)
		}
	}
	data, err := json.Marshal(program)
	if err != nil {
		return program, err
	}
	return ReadEducation(strings.NewReader(string(data)))
}

type EducationReport struct {
	ProgramID             string   `json:"program_id"`
	UnmatchedCompetencies int      `json:"unmatched_competencies"`
	UnmatchedKSA          int      `json:"unmatched_ksa"`
	UnknownTaxonomyCodes  []string `json:"unknown_taxonomy_codes"`
}

// SaveEducation не удаляет записи и связи, созданные при предыдущих загрузках.
func SaveEducation(ctx context.Context, pool *pgxpool.Pool, key, taxonomyID string, data EducationProgram) (EducationReport, error) {
	report := EducationReport{UnknownTaxonomyCodes: []string{}}
	key = strings.TrimSpace(key)
	if key == "" {
		return report, errors.New("нужен постоянный ключ программы")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return report, err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "education:"+key); err != nil {
		return report, err
	}
	var source string
	err = tx.QueryRow(ctx, `INSERT INTO sources(type,title,provider,external_id) VALUES('document',$1,'file',$2)
ON CONFLICT (provider,type,external_id) WHERE provider IS NOT NULL AND external_id IS NOT NULL
DO UPDATE SET title=EXCLUDED.title,updated_at=now() RETURNING id::text`, data.Title, "education:"+key).Scan(&source)
	if err != nil {
		return report, err
	}
	rows, err := tx.Query(ctx, "SELECT p.id::text FROM education_programs p JOIN program_sources s ON s.program_id=p.id WHERE s.source_id=$1 LIMIT 2", source)
	if err != nil {
		return report, err
	}
	var programs []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return report, err
		}
		programs = append(programs, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return report, err
	}
	rows.Close()
	if len(programs) > 1 {
		return report, errors.New("с источником связано несколько программ; автоматическое обновление невозможно")
	}
	if len(programs) == 0 {
		err = tx.QueryRow(ctx, "INSERT INTO education_programs(title,organization,code) VALUES($1,NULLIF($2,''),NULLIF($3,'')) RETURNING id::text", data.Title, strings.TrimSpace(data.Organization), strings.TrimSpace(data.Code)).Scan(&report.ProgramID)
		if err == nil {
			_, err = tx.Exec(ctx, "INSERT INTO program_sources(program_id,source_id) VALUES($1,$2)", report.ProgramID, source)
		}
	} else {
		report.ProgramID = programs[0]
		_, err = tx.Exec(ctx, "UPDATE education_programs SET title=$2,organization=NULLIF($3,''),code=NULLIF($4,''),updated_at=now() WHERE id=$1", report.ProgramID, data.Title, strings.TrimSpace(data.Organization), strings.TrimSpace(data.Code))
	}
	if err != nil {
		return report, err
	}
	unknown := map[string]bool{}
	resolve := func(code string) (string, error) {
		code = strings.TrimSpace(code)
		if code == "" {
			return "", errors.New("пустой код таксономии")
		}
		if taxonomyID == "" {
			unknown[code] = true
			return "", nil
		}
		var id string
		err := tx.QueryRow(ctx, "SELECT id::text FROM taxonomy_items WHERE taxonomy_id=$1 AND code=$2", taxonomyID, code).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			unknown[code] = true
			return "", nil
		}
		return id, err
	}
	for _, c := range data.Competencies {
		var cid string
		err = tx.QueryRow(ctx, `INSERT INTO program_competencies(program_id,code,title) VALUES($1,$2,$3)
ON CONFLICT (program_id,code) WHERE code IS NOT NULL DO UPDATE SET title=EXCLUDED.title,updated_at=now() RETURNING id::text`, report.ProgramID, c.Code, c.Title).Scan(&cid)
		if err != nil {
			return report, err
		}
		for _, code := range c.TaxonomyCodes {
			id, e := resolve(code)
			if e != nil {
				return report, e
			}
			if id != "" {
				if _, err = tx.Exec(ctx, "INSERT INTO competency_taxonomy_links(competency_id,taxonomy_item_id) VALUES($1,$2) ON CONFLICT DO NOTHING", cid, id); err != nil {
					return report, err
				}
			}
		}
		for _, k := range c.KSA {
			var kid string
			err = tx.QueryRow(ctx, "SELECT id::text FROM competency_ksa WHERE competency_id=$1 AND kind=$2 AND title=$3 ORDER BY id LIMIT 1", cid, k.Kind, k.Title).Scan(&kid)
			if errors.Is(err, pgx.ErrNoRows) {
				err = tx.QueryRow(ctx, "INSERT INTO competency_ksa(competency_id,kind,title) VALUES($1,$2,$3) RETURNING id::text", cid, k.Kind, k.Title).Scan(&kid)
			}
			if err != nil {
				return report, err
			}
			for _, code := range k.TaxonomyCodes {
				id, e := resolve(code)
				if e != nil {
					return report, e
				}
				if id != "" {
					if _, err = tx.Exec(ctx, "INSERT INTO ksa_taxonomy_links(ksa_id,taxonomy_item_id) VALUES($1,$2) ON CONFLICT DO NOTHING", kid, id); err != nil {
						return report, err
					}
				}
			}
		}
	}
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM program_competencies c WHERE c.program_id=$1 AND NOT EXISTS (SELECT 1 FROM competency_taxonomy_links l WHERE l.competency_id=c.id)", report.ProgramID).Scan(&report.UnmatchedCompetencies); err != nil {
		return report, err
	}
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM competency_ksa k JOIN program_competencies c ON c.id=k.competency_id WHERE c.program_id=$1 AND NOT EXISTS (SELECT 1 FROM ksa_taxonomy_links l WHERE l.ksa_id=k.id)", report.ProgramID).Scan(&report.UnmatchedKSA); err != nil {
		return report, err
	}
	for code := range unknown {
		report.UnknownTaxonomyCodes = append(report.UnknownTaxonomyCodes, code)
	}
	sort.Strings(report.UnknownTaxonomyCodes)
	if err = tx.Commit(ctx); err != nil {
		return report, err
	}
	return report, nil
}
