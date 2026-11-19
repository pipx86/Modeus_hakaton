package api

import (
	"context"
	"time"
)

// ListSourceQuality собирает признаки источников для админской оценки.
func (p *PGStore) ListSourceQuality(ctx context.Context) ([]sourceQualityItem, error) {
	rows, err := p.Pool.Query(ctx, `
		SELECT s.id::text, s.type::text, s.title,
			COALESCE(length(s.content), 0)::int,
			COALESCE(s.created_at, now()),
			COALESCE(sc.content_present, false),
			COALESCE(sc.origin_present, false),
			COALESCE(sc.url_format, 'missing'),
			COUNT(DISTINCT e.id)::int,
			COUNT(DISTINCT qc.evidence_id)::int,
			COUNT(DISTINCT qc.evidence_id) FILTER (WHERE qc.result = 'matched')::int
		FROM sources s
		LEFT JOIN source_checks sc ON sc.source_id = s.id
		LEFT JOIN evidence e ON e.source_id = s.id
		LEFT JOIN evidence_quote_checks qc ON qc.evidence_id = e.id
		WHERE s.run_id IS NULL
		GROUP BY s.id, sc.content_present, sc.origin_present, sc.url_format
		ORDER BY s.created_at DESC`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	items := []sourceQualityItem{}
	for rows.Next() {
		var item sourceQualityItem
		var createdAt time.Time
		if err := rows.Scan(
			&item.ID, &item.Type, &item.Title, &item.ContentChars, &createdAt,
			&item.ContentPresent, &item.OriginPresent, &item.URLFormat,
			&item.Citations, &item.QuoteChecked, &item.QuoteMatched,
		); err != nil {
			return nil, translate(err)
		}
		item.CreatedAt = createdAt
		items = append(items, item)
	}
	return items, rows.Err()
}
