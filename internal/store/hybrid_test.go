package store

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestСлияниеРанговПоиска(t *testing.T) {
	var a, b, c pgtype.UUID
	a.Bytes[15] = 1
	b.Bytes[15] = 2
	c.Bytes[15] = 3
	a.Valid = true
	b.Valid = true
	c.Valid = true
	result := fuseRanks([]SearchHit{{RefID: a, Score: 0.9}, {RefID: b, Score: 0.8}}, []SearchHit{{RefID: b, Score: 900}, {RefID: c, Score: 1}}, 3)
	if len(result) != 3 || result[0].RefID != b {
		t.Fatalf("общий результат не поднялся: %+v", result)
	}
	// Изменение исходных шкал не меняет результат слияния рангов.
	other := fuseRanks([]SearchHit{{RefID: a, Score: 1}, {RefID: b, Score: 0.01}}, []SearchHit{{RefID: b, Score: 0.001}, {RefID: c, Score: 0.0001}}, 1)
	if len(other) != 1 || other[0].RefID != b {
		t.Fatal(other)
	}
}
