package calendar

import "testing"

func TestRangeProblem(t *testing.T) {
	tests := []struct {
		name, from, to, want string
	}{
		{"one month", "2026-11-01", "2026-11-30", ""},
		{"single day", "2026-11-04", "2026-11-04", ""},
		{"missing from", "", "2026-11-30", "invalid from date"},
		{"bad to", "2026-11-01", "soon", "invalid to date"},
		{"reversed", "2026-11-30", "2026-11-01", "to must not be before from"},
		{"too long", "2026-01-01", "2028-01-01", "range is too long"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rangeProblem(tt.from, tt.to); got != tt.want {
				t.Errorf("rangeProblem() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNoteProblem(t *testing.T) {
	valid := func() NoteInput {
		return NoteInput{Date: "2026-11-04", Title: "Заметка", Color: "blue"}
	}
	goodTime, midnight, badTime := "09:30", "00:00", "25:00"

	tests := []struct {
		name   string
		change func(*NoteInput)
		want   string
	}{
		{"all day", func(*NoteInput) {}, ""},
		{"with time", func(input *NoteInput) { input.Time = &goodTime }, ""},
		{"at midnight", func(input *NoteInput) { input.Time = &midnight }, ""},
		{"bad date", func(input *NoteInput) { input.Date = "2026-02-30" }, "invalid date"},
		{"bad time", func(input *NoteInput) { input.Time = &badTime }, "invalid time"},
		{"blank title", func(input *NoteInput) { input.Title = "  " }, "title must be 1 to 200 characters long"},
		{"unknown color", func(input *NoteInput) { input.Color = "cyan" }, "unknown color"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := valid()
			tt.change(&input)
			if got := noteProblem(&input); got != tt.want {
				t.Errorf("noteProblem() = %q, want %q", got, tt.want)
			}
		})
	}
}
