package interviewer

import (
	"strings"
	"testing"
)

func TestCoursesForProfession(t *testing.T) {
	tests := []struct {
		profession string
		wantURL    string
	}{
		{profession: "Аналитик данных", wantURL: "https://stepik.org/course/300883/promo"},
		{profession: "Data Analyst (junior)", wantURL: "https://stepik.org/course/300883/promo"},
		{profession: "DevOps-инженер", wantURL: "https://stepik.org/course/227851/promo"},
		{profession: "Backend-разработчик", wantURL: "https://stepik.org/course/187490/promo"},
	}
	for _, tc := range tests {
		t.Run(tc.profession, func(t *testing.T) {
			courses := CoursesForProfession(tc.profession)
			if len(courses) != 2 {
				t.Fatalf("courses = %d, want 2", len(courses))
			}
			if !strings.Contains(courses[0].URL, tc.wantURL) {
				t.Fatalf("first URL = %q, want %q", courses[0].URL, tc.wantURL)
			}
		})
	}
}

func TestFinalCoursesMessage(t *testing.T) {
	message := FinalCoursesMessage("Аналитик данных")
	for _, want := range []string{"Интервью завершено", "SQL с нуля", "https://stepik.org/course/300883/promo", "BI"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message missing %q:\n%s", want, message)
		}
	}
	if got := FinalCoursesMessage("Повар"); !strings.Contains(got, "Интервью завершено") || strings.Contains(got, "Stepik") {
		t.Fatalf("unknown profession message = %q", got)
	}
}
