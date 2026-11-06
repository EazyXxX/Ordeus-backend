package kanban

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormalizeTasksKeepsAnEntryWhenModelReturnsNothing(t *testing.T) {
	got := normalizeTasks([]plannedTask{{Title: "  "}}, " Купить хлеб\nи молоко ")

	if len(got) != 1 {
		t.Fatalf("tasks = %d", len(got))
	}
	if got[0].Title != "Купить хлеб" || got[0].Description != "Купить хлеб\nи молоко" || got[0].Priority != "none" {
		t.Errorf("task = %+v", got[0])
	}
}

func TestNormalizeTasksCleansFields(t *testing.T) {
	got := normalizeTasks([]plannedTask{{
		Title:       " Позвонить ",
		Description: " В банк ",
		DueDate:     "к пятнице",
		Priority:    "очень важно",
	}, {
		Title:    "Отправить отчёт",
		DueDate:  " 2026-11-13 ",
		Priority: "high",
	}}, "текст")

	if got[0].Title != "Позвонить" || got[0].Description != "В банк" {
		t.Errorf("first = %+v", got[0])
	}
	if got[0].DueDate != "" || got[0].Priority != "none" {
		t.Errorf("invalid values kept: %+v", got[0])
	}
	if got[1].DueDate != "2026-11-13" || got[1].Priority != "high" {
		t.Errorf("valid values lost: %+v", got[1])
	}
}

func TestNormalizeTasksLimits(t *testing.T) {
	raw := make([]plannedTask, maxPlannedTasks+5)
	for i := range raw {
		raw[i].Title = strings.Repeat("я", maxTitleLength+10)
	}

	got := normalizeTasks(raw, "текст")

	if len(got) != maxPlannedTasks {
		t.Errorf("tasks = %d", len(got))
	}
	if utf8.RuneCountInString(got[0].Title) != maxTitleLength {
		t.Errorf("title length = %d", utf8.RuneCountInString(got[0].Title))
	}
}

func TestBuildPromptGivesTodayWithWeekday(t *testing.T) {
	prompt := buildPrompt("запись", "2026-11-06")
	for _, part := range []string{
		"2026-11-06, пятница",
		"2026-11-07 — суббота",
		"2026-11-13 — пятница",
		"<entry>\nзапись\n</entry>",
	} {
		if !strings.Contains(prompt, part) {
			t.Errorf("prompt lacks %q:\n%s", part, prompt)
		}
	}
}
