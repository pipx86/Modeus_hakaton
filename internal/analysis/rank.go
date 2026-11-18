package analysis

import (
	"math"
	"sort"
	"strings"
)

// SkillRank — навык с оценкой частоты появления и итоговой востребованности.
type SkillRank struct {
	Skill  string  `json:"skill"`
	Freq   int     `json:"freq"`   // сколько вакансий содержат навык
	Weight float64 `json:"weight"` // сырой вес (TF-подобная мера)
	Score  float64 `json:"score"`  // итоговый ранг: weight * log(freq+1)
	Rank   int     `json:"rank"`   // позиция после сортировки (1 = самый нужный)
}

// RankSkills ранжирует навыки по частоте появления и востребованности.
// Формула score = weight * log(freq + 1) совпадает с приоритетом в ComputeGap.
func RankSkills(freq map[string]int, weights map[string]float64) []SkillRank {
	if len(freq) == 0 {
		return nil
	}

	out := make([]SkillRank, 0, len(freq))
	for skill, f := range freq {
		w := 0.0
		if weights != nil {
			w = weights[skill]
		}
		score := w * math.Log(float64(f)+1)
		out = append(out, SkillRank{
			Skill:  skill,
			Freq:   f,
			Weight: w,
			Score:  score,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return strings.Compare(out[i].Skill, out[j].Skill) < 0
		}
		return out[i].Score > out[j].Score
	})

	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

// SkillPair — пара навыков и мера их «неразрывности».
type SkillPair struct {
	SkillA string  `json:"skill_a"`
	SkillB string  `json:"skill_b"`
	Count  int     `json:"count"` // сколько вакансий содержат оба навыка
	Score  float64 `json:"score"` // Jaccard = count / (freqA + freqB - count)
}

// CoOccurrenceResult содержит пары и матрицу affinity (skill → related → score).
type CoOccurrenceResult struct {
	Pairs    []SkillPair                   `json:"pairs"`
	Affinity map[string]map[string]float64 `json:"affinity"`
}

// ComputeCoOccurrence оценивает, насколько навыки неразрывны друг от друга.
// Берёт PerVacancy (списки навыков по вакансиям) и считает:
//   - Count — совместные появления;
//   - Score — Jaccard-сходство (0..1).
//
// Пары с count < minCount отбрасываются. Результат отсортирован по Score убыв.
func ComputeCoOccurrence(perVacancy [][]string, minCount int) CoOccurrenceResult {
	if minCount < 1 {
		minCount = 1
	}

	freq := map[string]int{}
	pairCount := map[string]map[string]int{}

	for _, skills := range perVacancy {
		seen := make(map[string]struct{}, len(skills))
		unique := make([]string, 0, len(skills))
		for _, s := range skills {
			s = normalizeSkill(s)
			if s == "" {
				continue
			}
			if _, ok := seen[s]; ok {
				continue
			}
			seen[s] = struct{}{}
			unique = append(unique, s)
			freq[s]++
		}

		for i := 0; i < len(unique); i++ {
			a := unique[i]
			for j := i + 1; j < len(unique); j++ {
				b := unique[j]
				x, y := a, b
				if strings.Compare(x, y) > 0 {
					x, y = y, x
				}
				if pairCount[x] == nil {
					pairCount[x] = map[string]int{}
				}
				pairCount[x][y]++
			}
		}
	}

	pairs := make([]SkillPair, 0)
	affinity := make(map[string]map[string]float64)

	for a, m := range pairCount {
		for b, cnt := range m {
			if cnt < minCount {
				continue
			}
			fa, fb := freq[a], freq[b]
			union := fa + fb - cnt
			score := 0.0
			if union > 0 {
				score = float64(cnt) / float64(union)
			}
			pairs = append(pairs, SkillPair{
				SkillA: a,
				SkillB: b,
				Count:  cnt,
				Score:  score,
			})

			if affinity[a] == nil {
				affinity[a] = map[string]float64{}
			}
			if affinity[b] == nil {
				affinity[b] = map[string]float64{}
			}
			affinity[a][b] = score
			affinity[b][a] = score
		}
	}

	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Score == pairs[j].Score {
			if pairs[i].Count == pairs[j].Count {
				return pairs[i].SkillA+pairs[i].SkillB < pairs[j].SkillA+pairs[j].SkillB
			}
			return pairs[i].Count > pairs[j].Count
		}
		return pairs[i].Score > pairs[j].Score
	})

	return CoOccurrenceResult{Pairs: pairs, Affinity: affinity}
}

// BoostPriorityByAffinity повышает приоритет навыка, если он часто встречается
// вместе с навыками, которые уже есть у кандидата.
// multiplier — насколько сильно учитывать affinity (обычно 0.1–0.5).
func BoostPriorityByAffinity(
	basePriority float64,
	skill string,
	candidateSkills []string,
	affinity map[string]map[string]float64,
	multiplier float64,
) float64 {
	if multiplier <= 0 || len(affinity) == 0 {
		return basePriority
	}
	rel, ok := affinity[normalizeSkill(skill)]
	if !ok {
		return basePriority
	}

	maxAff := 0.0
	for _, cs := range candidateSkills {
		cs = normalizeSkill(cs)
		if s, ok := rel[cs]; ok && s > maxAff {
			maxAff = s
		}
	}
	return basePriority * (1 + multiplier*maxAff)
}

// ---------------------------------------------------------------------------
// Кластеризация связанных (неразрывных) навыков
// ---------------------------------------------------------------------------

// SkillCluster — группа навыков, которые часто встречаются вместе
// или связаны через taxonomy.Related.
type SkillCluster struct {
	ID     string   `json:"id"`
	Skills []string `json:"skills"`
	Size   int      `json:"size"`
}

// ClusterRelatedSkills строит кластеры «неразрывных» навыков.
//
// Источники связей:
//  1. affinity-матрица из ComputeCoOccurrence (data-driven, Jaccard ≥ threshold);
//  2. taxonomy.Related (экспертные рёбра, вес 1.0).
//
// Алгоритм: неориентированный граф → connected components (DFS).
// threshold — минимальный Jaccard, чтобы считать навыки связанными (рекомендуется 0.25–0.4).
// Если taxonomy == nil, используются только co-occurrence рёбра.
func ClusterRelatedSkills(
	affinity map[string]map[string]float64,
	taxonomy *Taxonomy,
	threshold float64,
) []SkillCluster {
	if threshold <= 0 {
		threshold = 0.3
	}

	adj := map[string]map[string]struct{}{}
	addEdge := func(a, b string) {
		a, b = normalizeSkill(a), normalizeSkill(b)
		if a == "" || b == "" || a == b {
			return
		}
		if adj[a] == nil {
			adj[a] = map[string]struct{}{}
		}
		if adj[b] == nil {
			adj[b] = map[string]struct{}{}
		}
		adj[a][b] = struct{}{}
		adj[b][a] = struct{}{}
	}

	// 1. Co-occurrence рёбра
	for a, m := range affinity {
		for b, score := range m {
			if score >= threshold {
				addEdge(a, b)
			}
		}
	}

	// 2. Taxonomy.Related
	if taxonomy != nil {
		for _, def := range taxonomy.Definitions() {
			for _, rel := range def.Related {
				addEdge(def.Name, rel)
			}
		}
	}

	if len(adj) == 0 {
		return nil
	}

	// Connected components (DFS)
	visited := map[string]bool{}
	var components [][]string

	var dfs func(node string, comp *[]string)
	dfs = func(node string, comp *[]string) {
		visited[node] = true
		*comp = append(*comp, node)
		for neigh := range adj[node] {
			if !visited[neigh] {
				dfs(neigh, comp)
			}
		}
	}

	nodes := make([]string, 0, len(adj))
	for n := range adj {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)

	for _, n := range nodes {
		if visited[n] {
			continue
		}
		comp := []string{}
		dfs(n, &comp)
		sort.Strings(comp)
		components = append(components, comp)
	}

	clusters := make([]SkillCluster, 0, len(components))
	for _, comp := range components {
		if len(comp) < 2 {
			continue
		}
		clusters = append(clusters, SkillCluster{
			ID:     "cluster-" + comp[0],
			Skills: comp,
			Size:   len(comp),
		})
	}

	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].Size == clusters[j].Size {
			return clusters[i].ID < clusters[j].ID
		}
		return clusters[i].Size > clusters[j].Size
	})

	return clusters
}
