package recommend

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/domain"
)

// sampleNodes is a small, valid competency tree with one piece of evidence per
// node. It is used by the role/course tests where the level comes from the
// profession string.
func sampleNodes() []domain.Node {
	return []domain.Node{
		{
			ID:       "n1",
			Type:     domain.NodeRole,
			Title:    "Специалист",
			Evidence: []domain.Evidence{{SourceID: "s1", Quote: "отвечаю за направление"}},
		},
		{
			ID:       "n2",
			Type:     domain.NodeFunction,
			Title:    "Работа с данными",
			ParentID: ptr("n1"),
			Evidence: []domain.Evidence{{SourceID: "s1", Quote: "готовлю отчёты"}},
		},
		{
			ID:       "n3",
			Type:     domain.NodeTask,
			Title:    "SQL-запросы",
			ParentID: ptr("n2"),
			Evidence: []domain.Evidence{{SourceID: "s1", Quote: "пишу SQL-запросы"}},
		},
		{
			ID:       "n4",
			Type:     domain.NodeAction,
			Title:    "Собирать дашборды",
			ParentID: ptr("n3"),
			Evidence: []domain.Evidence{{SourceID: "s1", Quote: "строю дашборды"}},
		},
	}
}

// treeWithEvidence returns count action nodes, each backed by one quote.
func treeWithEvidence(count int) []domain.Node {
	nodes := make([]domain.Node, 0, count)
	for i := 0; i < count; i++ {
		nodes = append(nodes, domain.Node{
			ID:       fmt.Sprintf("n-%d", i),
			Type:     domain.NodeAction,
			Title:    fmt.Sprintf("Навык %d", i),
			Evidence: []domain.Evidence{{SourceID: "s1", Quote: fmt.Sprintf("цитата %d", i)}},
		})
	}
	return nodes
}

func ptr(s string) *string { return &s }

// TestBuildAllRoles checks the two-course selection and plan shape for every
// supported role: exactly two free Stepik courses, the right course order for
// the detected level, and 4-6 concrete Russian-language plan steps that
// reference both courses.
func TestBuildAllRoles(t *testing.T) {
	tests := []struct {
		name         string
		profession   string
		wantLevel    string
		wantFirstURL string
		wantCourses  []string
	}{
		{
			name:         "data analyst junior starts with SQL",
			profession:   "Аналитик данных (junior)",
			wantLevel:    LevelJunior,
			wantFirstURL: sqlCourseURL,
			wantCourses:  []string{sqlCourseTitle, biCourseTitle},
		},
		{
			name:         "data analyst middle starts with BI",
			profession:   "Data Analyst, middle",
			wantLevel:    LevelMiddle,
			wantFirstURL: biCourseURL,
			wantCourses:  []string{sqlCourseTitle, biCourseTitle},
		},
		{
			name:         "devops junior starts with DevOps basics",
			profession:   "DevOps (junior)",
			wantLevel:    LevelJunior,
			wantFirstURL: devopsCourseURL,
			wantCourses:  []string{devopsCourseTitle, dockerCourseTitle},
		},
		{
			name:         "devops senior starts with Docker",
			profession:   "Senior DevOps Engineer",
			wantLevel:    LevelSenior,
			wantFirstURL: dockerCourseURL,
			wantCourses:  []string{devopsCourseTitle, dockerCourseTitle},
		},
		{
			name:         "backend junior starts with Django",
			profession:   "Backend Developer (junior)",
			wantLevel:    LevelJunior,
			wantFirstURL: djangoCourseURL,
			wantCourses:  []string{djangoCourseTitle, goCourseTitle},
		},
		{
			name:         "backend middle starts with Go",
			profession:   "Backend-разработчик, middle",
			wantLevel:    LevelMiddle,
			wantFirstURL: goCourseURL,
			wantCourses:  []string{djangoCourseTitle, goCourseTitle},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := Build(tt.profession, sampleNodes(), nil)

			if rec.Profession != tt.profession {
				t.Fatalf("profession: got %q, want %q", rec.Profession, tt.profession)
			}
			if rec.Level != tt.wantLevel {
				t.Fatalf("level: got %q, want %q", rec.Level, tt.wantLevel)
			}
			if rec.GeneratedAt.IsZero() {
				t.Fatal("generated_at must be set")
			}
			if rec.Summary == "" {
				t.Fatal("summary must not be empty")
			}
			if len(rec.Courses) != 2 {
				t.Fatalf("courses: got %d, want exactly 2", len(rec.Courses))
			}

			gotTitles := make(map[string]bool, len(rec.Courses))
			for _, course := range rec.Courses {
				if course.Title == "" || course.URL == "" || course.Reason == "" {
					t.Fatalf("course fields must be filled: %+v", course)
				}
				if course.Provider != "Stepik" {
					t.Fatalf("provider: got %q, want Stepik", course.Provider)
				}
				if !course.Free {
					t.Fatalf("course %q must be marked free", course.Title)
				}
				if !strings.Contains(course.URL, "stepik.org/course/") {
					t.Fatalf("unexpected course URL: %q", course.URL)
				}
				if !strings.Contains(course.Reason, tt.wantLevel) {
					t.Fatalf("course reason must reference level %q: %q", tt.wantLevel, course.Reason)
				}
				gotTitles[course.Title] = true
			}
			for _, want := range tt.wantCourses {
				if !gotTitles[want] {
					t.Fatalf("missing course %q in %+v", want, rec.Courses)
				}
			}
			if rec.Courses[0].URL != tt.wantFirstURL {
				t.Fatalf("first course URL: got %q, want %q", rec.Courses[0].URL, tt.wantFirstURL)
			}

			if len(rec.Plan) < 4 || len(rec.Plan) > 6 {
				t.Fatalf("plan: got %d steps, want 4-6", len(rec.Plan))
			}
			var planText strings.Builder
			for i, step := range rec.Plan {
				if step.Order != i+1 {
					t.Fatalf("step %d: order = %d, want %d", i, step.Order, i+1)
				}
				if step.Done {
					t.Fatalf("step %d: done must start as false", i)
				}
				if step.Title == "" || step.Description == "" {
					t.Fatalf("step %d: title/description must be filled: %+v", i, step)
				}
				planText.WriteString(step.Title)
				planText.WriteString(" ")
				planText.WriteString(step.Description)
				planText.WriteString("\n")
			}
			for _, course := range rec.Courses {
				if !strings.Contains(planText.String(), course.Title) {
					t.Fatalf("plan does not include course %q as a resource", course.Title)
				}
			}
		})
	}
}

// TestBuildLevelDetection covers explicit Russian and English level clues and
// the answer/node-count heuristics that apply only when no clue is present.
func TestBuildLevelDetection(t *testing.T) {
	tests := []struct {
		name       string
		profession string
		nodes      []domain.Node
		answers    []domain.Evidence
		want       string
	}{
		{
			name:       "junior in english",
			profession: "Junior Backend Developer",
			want:       LevelJunior,
		},
		{
			name:       "junior in russian",
			profession: "Начинающий аналитик данных",
			want:       LevelJunior,
		},
		{
			name:       "junior trainee with yo normalized",
			profession: "Стажёр DevOps",
			want:       LevelJunior,
		},
		{
			name:       "junior one to three years",
			profession: "Backend Developer (1-3 года)",
			want:       LevelJunior,
		},
		{
			name:       "middle in english",
			profession: "Middle Backend Developer",
			want:       LevelMiddle,
		},
		{
			name:       "middle in russian",
			profession: "Аналитик данных (средний)",
			want:       LevelMiddle,
		},
		{
			name:       "senior in english uppercase",
			profession: "SENIOR DEVOPS ENGINEER",
			want:       LevelSenior,
		},
		{
			name:       "senior team lead",
			profession: "Team Lead Backend",
			want:       LevelSenior,
		},
		{
			name:       "senior in russian",
			profession: "Ведущий аналитик данных",
			want:       LevelSenior,
		},
		{
			name:       "heuristic junior by answer count",
			profession: "Аналитик данных",
			nodes:      treeWithEvidence(4),
			answers:    []domain.Evidence{{SourceID: "s1", Quote: "q1"}, {SourceID: "s1", Quote: "q2"}},
			want:       LevelJunior,
		},
		{
			name:       "heuristic junior by node count",
			profession: "Аналитик данных",
			nodes:      treeWithEvidence(3),
			answers:    []domain.Evidence{{SourceID: "s1", Quote: "q1"}, {SourceID: "s1", Quote: "q2"}, {SourceID: "s1", Quote: "q3"}},
			want:       LevelJunior,
		},
		{
			name:       "heuristic middle by large tree",
			profession: "Аналитик данных",
			nodes:      treeWithEvidence(8),
			answers:    []domain.Evidence{{SourceID: "s1", Quote: "q1"}, {SourceID: "s1", Quote: "q2"}, {SourceID: "s1", Quote: "q3"}, {SourceID: "s1", Quote: "q4"}, {SourceID: "s1", Quote: "q5"}, {SourceID: "s1", Quote: "q6"}, {SourceID: "s1", Quote: "q7"}, {SourceID: "s1", Quote: "q8"}},
			want:       LevelMiddle,
		},
		{
			name:       "heuristic unknown for medium run",
			profession: "Аналитик данных",
			nodes:      treeWithEvidence(5),
			answers:    []domain.Evidence{{SourceID: "s1", Quote: "q1"}, {SourceID: "s1", Quote: "q2"}, {SourceID: "s1", Quote: "q3"}, {SourceID: "s1", Quote: "q4"}, {SourceID: "s1", Quote: "q5"}},
			want:       LevelUnknown,
		},
		{
			name:       "explicit senior wins over small run",
			profession: "Senior DevOps",
			nodes:      treeWithEvidence(1),
			want:       LevelSenior,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := Build(tt.profession, tt.nodes, tt.answers)
			if rec.Level != tt.want {
				t.Fatalf("level: got %q, want %q (summary=%q)", rec.Level, tt.want, rec.Summary)
			}
		})
	}
}

// TestDetectRole checks Russian/English/case-insensitive role matching.
func TestDetectRole(t *testing.T) {
	tests := []struct {
		profession string
		want       string
	}{
		{"Data Analyst / Аналитик данных", RoleDataAnalyst},
		{"аналитик данных", RoleDataAnalyst},
		{"ANALYST", RoleDataAnalyst},
		{"DevOps инженер", RoleDevOps},
		{"девопс", RoleDevOps},
		{"SRE", RoleDevOps},
		{"Backend Developer", RoleBackend},
		{"бэкенд-разработчик", RoleBackend},
		{"Разработчик Python", RoleBackend},
		{"Повар", RoleUnknown},
		{"", RoleUnknown},
	}

	for _, tt := range tests {
		if got := DetectRole(tt.profession); got != tt.want {
			t.Errorf("DetectRole(%q) = %q, want %q", tt.profession, got, tt.want)
		}
	}
}

// TestBuildUnknownRole verifies the safe fallback: no unsupported courses, a
// generic Russian plan, and a summary that explains what to do next.
func TestBuildUnknownRole(t *testing.T) {
	rec := Build("Повар", sampleNodes(), nil)

	if len(rec.Courses) != 0 {
		t.Fatalf("unknown role must not receive courses, got %+v", rec.Courses)
	}
	if rec.Courses == nil {
		t.Fatal("courses must encode as an empty JSON array, not null")
	}
	if len(rec.Plan) < 4 || len(rec.Plan) > 6 {
		t.Fatalf("plan: got %d steps, want 4-6", len(rec.Plan))
	}
	if !strings.Contains(rec.Summary, "не сопоставлена") {
		t.Fatalf("summary must explain the unknown role: %q", rec.Summary)
	}
	for _, step := range rec.Plan {
		if strings.Contains(step.Description, "stepik.org") {
			t.Fatalf("unknown role plan must not reference unsupported courses: %+v", step)
		}
	}
}

// TestBuildEmptyInputs guarantees a usable fallback for a degraded run with no
// profession, tree or answers.
func TestBuildEmptyInputs(t *testing.T) {
	rec := Build("", nil, nil)

	if rec.Profession != "" {
		t.Fatalf("profession: got %q, want empty", rec.Profession)
	}
	if rec.Level != LevelJunior {
		t.Fatalf("level: got %q, want %q for an empty run", rec.Level, LevelJunior)
	}
	if len(rec.Courses) != 0 {
		t.Fatalf("empty run must not receive courses, got %+v", rec.Courses)
	}
	if len(rec.Plan) < 4 || len(rec.Plan) > 6 {
		t.Fatalf("plan: got %d steps, want 4-6", len(rec.Plan))
	}
	if !strings.Contains(rec.Summary, "не указана") {
		t.Fatalf("summary must mention the missing profession: %q", rec.Summary)
	}
}

// TestBuildDeterministic proves the generated content is a pure function of the
// inputs: only generated_at is allowed to differ between two calls.
func TestBuildDeterministic(t *testing.T) {
	nodes := sampleNodes()
	answers := []domain.Evidence{{SourceID: "s1", Quote: "пишу SQL-запросы"}}

	first := Build("Analyst (middle)", nodes, answers)
	second := Build("Analyst (middle)", nodes, answers)

	first.GeneratedAt = time.Time{}
	second.GeneratedAt = time.Time{}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Build must be deterministic:\nfirst=%+v\nsecond=%+v", first, second)
	}
}

// TestBuildReasonsReferenceGaps checks that course reasons and plan steps
// mention a concrete competency gap from the tree.
func TestBuildReasonsReferenceGaps(t *testing.T) {
	nodes := []domain.Node{
		{
			ID:       "n1",
			Type:     domain.NodeTask,
			Title:    "SQL-запросы",
			Evidence: nil,
		},
		{
			ID:       "n2",
			Type:     domain.NodeAction,
			Title:    "Дашборды",
			ParentID: ptr("n1"),
			Evidence: []domain.Evidence{{SourceID: "s1", Quote: "строю дашборды"}},
		},
	}

	rec := Build("Аналитик данных (junior)", nodes, nil)

	var coursesText, planText strings.Builder
	for _, course := range rec.Courses {
		coursesText.WriteString(course.Reason)
		coursesText.WriteString("\n")
	}
	for _, step := range rec.Plan {
		planText.WriteString(step.Description)
		planText.WriteString("\n")
	}

	if !strings.Contains(coursesText.String(), "SQL-запросы") {
		t.Fatalf("course reasons must reference the gap, got:\n%s", coursesText.String())
	}
	if !strings.Contains(planText.String(), "SQL-запросы") {
		t.Fatalf("plan must reference the gap, got:\n%s", planText.String())
	}
}
