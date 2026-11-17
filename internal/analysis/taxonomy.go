package analysis

import (
	"strings"
	"unicode"
)

type SkillDefinition struct {
	Name       string
	Aliases    []string
	Categories []string
	Related    []string
	Parent     string
}

type Taxonomy struct {
	byAlias map[string]string
	byName  map[string]SkillDefinition
}

func NewTaxonomy(definitions []SkillDefinition) *Taxonomy {
	t := &Taxonomy{
		byAlias: make(map[string]string),
		byName:  make(map[string]SkillDefinition),
	}
	for _, d := range definitions {
		name := normalizeSkill(d.Name)
		if name == "" {
			continue
		}
		d.Name = name
		t.byName[name] = d
		t.byAlias[name] = name
		for _, alias := range d.Aliases {
			if key := normalizeSkill(alias); key != "" {
				t.byAlias[key] = name
			}
		}
	}
	return t
}

func DefaultTaxonomy() *Taxonomy {
	return NewTaxonomy([]SkillDefinition{

		{Name: "go", Aliases: []string{"golang", "язык go"}, Categories: []string{"BackEnd"}, Related: []string{"gin", "echo", "grpc", "postgresql", "docker"}},
		{Name: "git", Aliases: []string{"git version control"}, Categories: []string{"Development", "DevOps"}, Related: []string{"github", "gitlab", "ci/cd"}},
		{Name: "python", Aliases: []string{"python programming", "python разработка"}, Categories: []string{"BackEnd", "Data Analyst", "Machine Learning"}, Related: []string{"django", "fastapi", "flask", "pandas", "numpy", "pytorch"}},
		{Name: "java", Aliases: []string{"enterprise java"}, Categories: []string{"BackEnd"}, Related: []string{"spring", "spring boot", "hibernate", "maven", "gradle"}},
		{Name: "c#", Aliases: []string{"csharp", "c sharp"}, Categories: []string{"BackEnd"}, Related: []string{".net", "asp.net core"}},
		{Name: ".net", Aliases: []string{"dotnet", ".net core", "net core"}, Categories: []string{"BackEnd"}, Related: []string{"c#", "asp.net core", "entity framework"}},
		{Name: "sql", Aliases: []string{"sql queries", "sql запросы"}, Categories: []string{"BackEnd", "Data Analyst"}, Related: []string{"postgresql", "mysql", "sqlite", "sql server"}},
		{Name: "postgresql", Aliases: []string{"postgres", "postgres db"}, Categories: []string{"BackEnd", "Data Analyst", "DevOps"}, Related: []string{"sql", "pgx", "database indexing"}},
		{Name: "mysql", Aliases: []string{"mariadb"}, Categories: []string{"BackEnd", "Data Analyst"}, Related: []string{"sql"}},
		{Name: "redis", Categories: []string{"BackEnd", "DevOps"}, Related: []string{"caching", "go", "python"}},
		{Name: "docker", Aliases: []string{"containerization", "контейнеризация"}, Categories: []string{"DevOps"}, Related: []string{"docker compose", "kubernetes", "helm", "containerd"}},
		{Name: "docker compose", Aliases: []string{"docker-compose"}, Categories: []string{"DevOps"}, Related: []string{"docker"}},
		{Name: "kubernetes", Aliases: []string{"k8s"}, Categories: []string{"DevOps"}, Related: []string{"docker", "helm", "kustomize", "istio"}},
		{Name: "helm", Aliases: []string{"helm charts"}, Categories: []string{"DevOps"}, Related: []string{"kubernetes"}},
		{Name: "terraform", Aliases: []string{"iac", "infrastructure as code"}, Categories: []string{"DevOps"}, Related: []string{"ansible", "aws", "azure", "gcp"}},
		{Name: "ansible", Categories: []string{"DevOps"}, Related: []string{"terraform", "linux"}},
		{Name: "linux", Aliases: []string{"ubuntu", "unix"}, Categories: []string{"DevOps", "System Administration"}, Related: []string{"bash", "docker", "systemd", "networking"}},
		{Name: "bash", Aliases: []string{"shell scripting", "bash scripting"}, Categories: []string{"DevOps", "System Administration"}, Related: []string{"linux"}},
		{Name: "ci/cd", Aliases: []string{"continuous integration", "continuous delivery", "gitlab ci/cd", "gitlab ci"}, Categories: []string{"DevOps"}, Related: []string{"jenkins", "github actions", "gitlab ci", "docker"}},
		{Name: "prometheus", Categories: []string{"DevOps"}, Related: []string{"grafana", "alertmanager", "opentelemetry"}},
		{Name: "grafana", Categories: []string{"DevOps", "Data Analyst"}, Related: []string{"prometheus", "loki"}},
		{Name: "aws", Aliases: []string{"amazon web services"}, Categories: []string{"DevOps", "Cloud"}, Related: []string{"terraform", "docker", "kubernetes"}},
		{Name: "azure", Aliases: []string{"microsoft azure"}, Categories: []string{"DevOps", "Cloud"}, Related: []string{"terraform", "kubernetes"}},
		{Name: "gcp", Aliases: []string{"google cloud", "google cloud platform"}, Categories: []string{"DevOps", "Cloud"}, Related: []string{"terraform", "kubernetes"}},
		{Name: "pandas", Aliases: []string{"pandas python"}, Categories: []string{"Data Analyst", "Machine Learning"}, Related: []string{"python", "numpy"}},
		{Name: "numpy", Aliases: []string{"numpy python"}, Categories: []string{"Data Analyst", "Machine Learning"}, Related: []string{"python", "pandas"}},
		{Name: "tableau", Categories: []string{"Data Analyst"}, Related: []string{"sql", "data visualization"}},
		{Name: "power bi", Aliases: []string{"powerbi"}, Categories: []string{"Data Analyst"}, Related: []string{"sql", "data visualization"}},
		{Name: "machine learning", Aliases: []string{"ml", "машинное обучение"}, Categories: []string{"Machine Learning", "Data Science"}, Related: []string{"python", "scikit-learn", "pytorch", "tensorflow"}},
		{Name: "deep learning", Aliases: []string{"глубокое обучение"}, Categories: []string{"Machine Learning", "Data Science"}, Related: []string{"pytorch", "tensorflow", "transformers"}},
		{Name: "nlp", Aliases: []string{"natural language processing", "обработка естественного языка"}, Categories: []string{"Machine Learning", "Data Science"}, Related: []string{"transformers", "llm", "rag"}},
		{Name: "llm", Aliases: []string{"large language models", "большие языковые модели"}, Categories: []string{"AI", "Machine Learning"}, Related: []string{"rag", "embeddings", "langchain", "prompt engineering"}},
		{Name: "rag", Aliases: []string{"retrieval augmented generation"}, Categories: []string{"AI"}, Related: []string{"llm", "embeddings", "vector databases"}},
		{Name: "embeddings", Aliases: []string{"word embeddings", "эмбеддинги"}, Categories: []string{"AI", "Machine Learning"}, Related: []string{"rag", "vector databases"}},
		{Name: "react", Aliases: []string{"react.js", "reactjs"}, Categories: []string{"Frontend"}, Related: []string{"javascript", "typescript", "next.js"}},
		{Name: "javascript", Aliases: []string{"js"}, Categories: []string{"Frontend", "Backend"}, Related: []string{"react", "node.js", "typescript"}},
		{Name: "typescript", Aliases: []string{"ts"}, Categories: []string{"Frontend", "Backend"}, Related: []string{"javascript", "react", "node.js"}},
		{Name: "html", Aliases: []string{"html5"}, Categories: []string{"Frontend"}, Related: []string{"css", "javascript"}},
		{Name: "css", Aliases: []string{"css3"}, Categories: []string{"Frontend"}, Related: []string{"html", "javascript"}},
	})
}

func (t *Taxonomy) Canonical(name string) string {
	if t == nil {
		return normalizeSkill(name)
	}
	key := normalizeSkill(name)
	if canonical, ok := t.byAlias[key]; ok {
		return canonical
	}
	return key
}

func (t *Taxonomy) Definition(name string) (SkillDefinition, bool) {
	if t == nil {
		return SkillDefinition{}, false
	}
	d, ok := t.byName[t.Canonical(name)]
	return d, ok
}

func (t *Taxonomy) Categories(name string) []string {
	d, ok := t.Definition(name)
	if !ok {
		return nil
	}
	out := make([]string, len(d.Categories))
	copy(out, d.Categories)
	return out
}

func (t *Taxonomy) Related(name string) []string {
	d, ok := t.Definition(name)
	if !ok {
		return nil
	}
	out := make([]string, len(d.Related))
	copy(out, d.Related)
	return out
}

func normalizeSkill(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	return strings.Join(strings.Fields(s), " ")
}

func hasCategory(categories []string, target string) bool {
	for _, c := range categories {
		if strings.EqualFold(c, target) {
			return true
		}
	}
	return false
}

func containsWholeWord(text, phrase string) bool {
	text = " " + strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '+' || r == '#' || r == '.' {
			return unicode.ToLower(r)
		}
		return ' '
	}, text) + " "
	phrase = " " + normalizeSkill(phrase) + " "
	return strings.Contains(text, phrase)
}
func (t *Taxonomy) Definitions() []SkillDefinition {
	definitions := make([]SkillDefinition, 0, len(t.byName))

	for _, definition := range t.byName {
		definitions = append(definitions, definition)
	}

	return definitions
}
