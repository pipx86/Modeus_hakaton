package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
)

const MarketSkillSeries = "hh_vacancies"

type SkillSnapshot struct {
	Series      string
	Month       time.Time
	Frequencies map[string]float64
}

func ReadSkillSnapshot(r io.Reader, filename string) (SkillSnapshot, error) {
	var snapshot SkillSnapshot
	data, err := io.ReadAll(io.LimitReader(r, 20<<20+1))
	if err != nil {
		return snapshot, err
	}
	if len(data) > 20<<20 {
		return snapshot, errors.New("файл истории больше 20 МБ")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || len(fields) == 0 {
		return snapshot, errors.New("ожидается объект с частотами навыков")
	}
	var meta struct {
		SnapshotDate string `json:"snapshot_date"`
		Profession   string `json:"profession"`
		Synthetic    bool   `json:"synthetic"`
	}
	if raw, ok := fields["_meta"]; ok {
		if err := json.Unmarshal(raw, &meta); err != nil {
			return snapshot, fmt.Errorf("дата и серия снимка: %w", err)
		}
		delete(fields, "_meta")
	}
	if meta.Synthetic {
		return snapshot, errors.New("синтетический снимок нельзя использовать как наблюдение")
	}
	stamp := meta.SnapshotDate
	if stamp == "" {
		base := strings.TrimSuffix(filepath.Base(filename), ".json")
		if len(base) >= 10 {
			stamp = base[len(base)-10:]
			if _, err := time.Parse(time.DateOnly, stamp); err != nil {
				stamp = base[len(base)-7:]
			}
		}
	}
	date, err := time.Parse(time.DateOnly, stamp)
	if err != nil {
		date, err = time.Parse("2006-01", stamp)
		if err != nil {
			return snapshot, errors.New("нужен месяц YYYY-MM или дата YYYY-MM-DD")
		}
	}
	if date.Year() < 1 {
		return snapshot, errors.New("год снимка должен быть положительным")
	}
	snapshot.Month = time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, time.UTC)
	snapshot.Series = MarketSkillSeries
	if name := strings.TrimSpace(meta.Profession); name != "" {
		if !utf8.ValidString(name) || len(name) > 256 || strings.ContainsRune(name, 0) {
			return snapshot, errors.New("недопустимое название серии")
		}
		snapshot.Series = "profession_" + name
	}
	snapshot.Frequencies = make(map[string]float64, len(fields))
	for skill, raw := range fields {
		var frequency *float64
		if err := json.Unmarshal(raw, &frequency); err != nil || frequency == nil ||
			math.IsNaN(*frequency) || math.IsInf(*frequency, 0) || *frequency < 0 || *frequency > 1e15 ||
			strings.TrimSpace(skill) == "" || strings.ContainsRune(skill, 0) {
			return SkillSnapshot{}, fmt.Errorf("неверная частота навыка %q", skill)
		}
		snapshot.Frequencies[skill] = *frequency
	}
	if len(snapshot.Frequencies) == 0 {
		return SkillSnapshot{}, errors.New("в снимке нет частот")
	}
	return snapshot, nil
}

func ImportSkillSnapshots(ctx context.Context, pool transactionPool, files []string) (int64, error) {
	snapshots := make([]SkillSnapshot, 0, len(files))
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return 0, err
		}
		snapshot, readErr := ReadSkillSnapshot(f, path)
		f.Close()
		if readErr != nil {
			return 0, fmt.Errorf("%s: %w", filepath.Base(path), readErr)
		}
		snapshots = append(snapshots, snapshot)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(context.Background())
	queries := db.New(tx)
	var changed int64
	for _, snapshot := range snapshots {
		frequencies, err := json.Marshal(snapshot.Frequencies)
		if err != nil {
			return 0, err
		}
		written, err := queries.UpsertSkillFrequencySnapshot(ctx, db.UpsertSkillFrequencySnapshotParams{
			Series: snapshot.Series, SnapshotMonth: pgtype.Date{Time: snapshot.Month, Valid: true}, Frequencies: frequencies,
		})
		if err != nil {
			return 0, err
		}
		changed += written
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return changed, nil
}
