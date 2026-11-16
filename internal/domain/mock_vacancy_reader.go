package domain

import "context"

type MockVacancyReader struct {
	Data map[string][]VacancyRecord
}

func NewMockVacancyReader() *MockVacancyReader {
	return &MockVacancyReader{Data: map[string][]VacancyRecord{}}
}

func (m *MockVacancyReader) Add(profession string, records ...VacancyRecord) {
	m.Data[profession] = append(m.Data[profession], records...)
}

func (m *MockVacancyReader) ByProfession(_ context.Context, profession string, _ int) ([]VacancyRecord, error) {
	return m.Data[profession], nil
}
