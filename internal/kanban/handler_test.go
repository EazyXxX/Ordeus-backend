package kanban

import "testing"

func validInput() CardInput {
	return CardInput{Title: "Задача", Priority: "none"}
}

func TestCardProblem(t *testing.T) {
	badDate := "2026-13-40"
	goodDate := "2026-11-04"

	tests := []struct {
		name   string
		change func(*CardInput)
		want   string
	}{
		{"minimal card", func(*CardInput) {}, ""},
		{"full card", func(input *CardInput) {
			input.Color = "blue"
			input.Priority = "urgent"
			input.DueDate = &goodDate
		}, ""},
		{"unknown color", func(input *CardInput) { input.Color = "#fff" }, "unknown color"},
		{"unknown priority", func(input *CardInput) { input.Priority = "" }, "unknown priority"},
		{"invalid date", func(input *CardInput) { input.DueDate = &badDate }, "invalid due date"},
		{"too many items", func(input *CardInput) {
			input.Checklist = make([]ChecklistItem, maxChecklistItems+1)
			for i := range input.Checklist {
				input.Checklist[i].Title = "x"
			}
		}, "checklist is too long"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validInput()
			tt.change(&input)
			if got := cardProblem(&input); got != tt.want {
				t.Errorf("cardProblem() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCleanChecklistDropsEmptyItemsAndIsNeverNil(t *testing.T) {
	got := cleanChecklist([]ChecklistItem{{Title: "  шаг ", Done: true}, {Title: "   "}})
	if len(got) != 1 || got[0].Title != "шаг" || !got[0].Done {
		t.Errorf("got %+v", got)
	}
	if cleanChecklist(nil) == nil {
		t.Error("expected an empty slice, not nil")
	}
}
