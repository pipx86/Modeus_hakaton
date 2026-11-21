package fgos

import (
	"sort"
	"strings"

	"activity-descriptor/internal/ingestion"
)

// MergeResult — итог добавления извлечённых KSA в черновик программы.
// Added — сколько KSA реально дописано; AlreadyPresent — сколько уже было в
// программе (включая повторы внутри входных данных); UnknownCodes — коды
// компетенций, которых в программе нет (отсортированы и без дублей).
type MergeResult struct {
	Added          int      `json:"added"`
	AlreadyPresent int      `json:"already_present"`
	UnknownCodes   []string `json:"unknown_codes"`
}

// MergeKSA дописывает извлечённые KSA в существующий черновик программы по
// коду компетенции.
//
// Формулировки, типы и порядок компетенций не меняются: изменяются только
// массивы KSA у совпавших кодов. Дубли определяются по (code, kind, title):
// нормализованные code и kind взяты из ExtractKSA, title сравнивается после
// TrimSpace. KSA с неизвестным видом или пустым title пропускаются — они не
// должны попасть в программу даже при вызове MergeKSA напрямую.
//
// Коды из entries нормализуются так же, как при извлечении, поэтому «опк-2» и
// «ОПК-2» совпадают. Коды, которых нет в программе, возвращаются в
// UnknownCodes и не создают новых компетенций: добавлять то, чего нет в
// программе, — решение человека, а не автоматического слияния.
func MergeKSA(program *ingestion.EducationProgram, entries []KSAEntry) MergeResult {
	result := MergeResult{UnknownCodes: []string{}}
	if program == nil {
		for _, entry := range entries {
			code := normalizeKSACode(entry.Code)
			if len(entry.KSA) > 0 && competencyCodePattern.MatchString(code) {
				result.addUnknown(code)
			}
		}
		result.sortUnknown()
		return result
	}

	indexByCode := make(map[string]int, len(program.Competencies))
	for index := range program.Competencies {
		indexByCode[normalizeKSACode(program.Competencies[index].Code)] = index
	}

	for _, entry := range entries {
		code := normalizeKSACode(entry.Code)
		if !competencyCodePattern.MatchString(code) {
			continue
		}
		index, found := indexByCode[code]
		if !found {
			if len(entry.KSA) > 0 {
				result.addUnknown(code)
			}
			continue
		}
		competency := &program.Competencies[index]
		seen := make(map[string]bool, len(competency.KSA)+len(entry.KSA))
		for _, item := range competency.KSA {
			seen[item.Kind+"\x00"+item.Title] = true
		}
		for _, item := range entry.KSA {
			kind, kindOK := normalizeKSAKind(item.Kind)
			if !kindOK {
				continue
			}
			title := strings.TrimSpace(item.Title)
			if title == "" {
				continue
			}
			key := kind + "\x00" + title
			if seen[key] {
				result.AlreadyPresent++
				continue
			}
			seen[key] = true
			merged := item
			merged.Kind = kind
			merged.Title = title
			competency.KSA = append(competency.KSA, merged)
			result.Added++
		}
	}
	result.sortUnknown()
	return result
}

// addUnknown запоминает код, которого нет в программе, без дублей.
func (r *MergeResult) addUnknown(code string) {
	for _, existing := range r.UnknownCodes {
		if existing == code {
			return
		}
	}
	r.UnknownCodes = append(r.UnknownCodes, code)
}

// sortUnknown делает unknown_codes детерминированными независимо от порядка
// чанков и записей.
func (r *MergeResult) sortUnknown() {
	if len(r.UnknownCodes) == 0 {
		r.UnknownCodes = []string{}
		return
	}
	sort.Strings(r.UnknownCodes)
}
