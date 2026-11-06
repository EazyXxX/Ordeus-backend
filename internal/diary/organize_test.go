package diary

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestNormalizePlanFallsBackWhenModelReturnsNothing(t *testing.T) {
	got := normalizePlan(plan{Notes: []plannedNote{{Body: "   "}}}, " Купить хлеб\nи молоко ", 0)

	if len(got.Notes) != 1 {
		t.Fatalf("notes = %d", len(got.Notes))
	}
	note := got.Notes[0]
	if note.Body != "Купить хлеб\nи молоко" || note.Title != "Купить хлеб" || note.Folder != fallbackFolder {
		t.Errorf("note = %+v", note)
	}
}

func TestNormalizePlanCleansFields(t *testing.T) {
	raw := plan{Notes: []plannedNote{{
		Title:  "  Идея ",
		Body:   " текст ",
		Tags:   []string{"#Работа", "работа", " ", "a", "b", "c", "d", "e"},
		Folder: "",
	}}}

	note := normalizePlan(raw, "текст", 0).Notes[0]

	if note.Title != "Идея" || note.Body != "текст" || note.Folder != fallbackFolder {
		t.Errorf("note = %+v", note)
	}
	if want := []string{"работа", "a", "b", "c", "d"}; !slices.Equal(note.Tags, want) {
		t.Errorf("tags = %v, want %v", note.Tags, want)
	}
}

func TestNormalizePlanFixesReferences(t *testing.T) {
	raw := plan{Notes: []plannedNote{
		{Body: "первая", RelatedExisting: []int{0, 1, 2, 2, 9}, RelatedNew: []int{0, 2, 7}},
		{Body: "  "},
		{Body: "третья", RelatedNew: []int{0, 1}},
	}}

	got := normalizePlan(raw, "текст", 2).Notes

	if len(got) != 2 {
		t.Fatalf("notes = %d", len(got))
	}
	if !slices.Equal(got[0].RelatedExisting, []int{1, 2}) {
		t.Errorf("existing = %v", got[0].RelatedExisting)
	}
	if !slices.Equal(got[0].RelatedNew, []int{1}) {
		t.Errorf("related of first = %v", got[0].RelatedNew)
	}
	if !slices.Equal(got[1].RelatedNew, []int{0}) {
		t.Errorf("related of last = %v", got[1].RelatedNew)
	}
}

func TestNormalizePlanKeepsExactTextOfSingleNote(t *testing.T) {
	raw := plan{Notes: []plannedNote{{Title: "Планы", Body: "Хочу выучить испанский кr лету"}}}

	got := normalizePlan(raw, " Хочу выучить испанский к лету ", 0)

	if body := got.Notes[0].Body; body != "Хочу выучить испанский к лету" {
		t.Errorf("body = %q", body)
	}
}

func TestNormalizePlanKeepsBodiesOfSplitNotes(t *testing.T) {
	raw := plan{Notes: []plannedNote{{Body: "первая"}, {Body: "вторая"}}}

	got := normalizePlan(raw, "Раздели на заметки. первая вторая", 0)

	if got.Notes[0].Body != "первая" || got.Notes[1].Body != "вторая" {
		t.Errorf("notes = %+v", got.Notes)
	}
}

func TestOrganizerPromptSplitsOnlyOnRequest(t *testing.T) {
	for _, rule := range []string{"По умолчанию вся запись становится одной заметкой", "автор прямо просит"} {
		if !strings.Contains(organizerPrompt, rule) {
			t.Errorf("prompt lacks %q", rule)
		}
	}
}

func TestApplyEntryTextTakesHeadingFromFirstLine(t *testing.T) {
	tests := []struct {
		name, title, text, wantTitle, wantBody string
	}{
		{"exact copy", "Рецепт пирога", "Рецепт пирога\nПробовал новый рецепт.", "Рецепт пирога", "Пробовал новый рецепт."},
		{"keeps author spelling", "рецепт ПИРОГА", "Рецепт пирога:\n\nТекст.", "Рецепт пирога:", "Текст."},
		{"markdown heading", "План на неделю", "# План на неделю\nТекст", "# План на неделю", "Текст"},
		{"model made its own title", "Удачный пирог", "Рецепт пирога\nТекст.", "Удачный пирог", "Рецепт пирога\nТекст."},
		{"single line stays body", "Купить молоко", "Купить молоко", "Купить молоко", "Купить молоко"},
		{"heading without text", "Заметка", "Заметка\n   ", "Заметка", "Заметка"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyEntryText(plannedNote{Title: tt.title}, tt.text)
			if got.Title != tt.wantTitle || got.Body != tt.wantBody {
				t.Errorf("got %q / %q, want %q / %q", got.Title, got.Body, tt.wantTitle, tt.wantBody)
			}
		})
	}
}

func TestWithValidDailyDates(t *testing.T) {
	const today = "2026-11-06"
	for date, want := range map[string]string{
		"":             "",
		"2026-11-06":   "2026-11-06",
		" 2026-11-05 ": "2026-11-05",
		"2026-11-07":   "",
		"сегодня":      "",
		"2026-02-30":   "",
	} {
		got := withValidDailyDates([]plannedNote{{DailyDate: date}}, today)[0].DailyDate
		if got != want {
			t.Errorf("%q = %q, want %q", date, got, want)
		}
	}
}

func TestNormalizePlanLimitsNotes(t *testing.T) {
	raw := plan{}
	for range maxPlannedNotes + 3 {
		raw.Notes = append(raw.Notes, plannedNote{Body: "x"})
	}
	if got := normalizePlan(raw, "x", 0); len(got.Notes) != maxPlannedNotes {
		t.Errorf("notes = %d", len(got.Notes))
	}
}

func TestValidEmoji(t *testing.T) {
	for _, emoji := range []string{"💼", "❤️", "🏃‍♂️", "👨‍👩‍👧‍👦", "🇷🇺", "👍🏽", "↔️", "ℹ️", "1️⃣", "#️⃣", "🏴󠁧󠁢󠁥󠁮󠁧󠁿"} {
		if !validEmoji(emoji) {
			t.Errorf("validEmoji(%q) = false", emoji)
		}
	}
	for _, text := range []string{"", "работа", "a", "💼 работа", "<b>", " ", "\u200d"} {
		if validEmoji(text) {
			t.Errorf("validEmoji(%q) = true", text)
		}
	}
}

func TestNormalizePlanCleansEmojis(t *testing.T) {
	result := normalizePlan(plan{Notes: []plannedNote{{
		Body:        "текст",
		Tags:        []string{"Идея", "#дом"},
		FolderEmoji: " слово ",
		TagEmojis: []tagEmoji{
			{Tag: "#ИДЕЯ", Emoji: " 💡 "},
			{Tag: "идея", Emoji: "🔥"},
			{Tag: "дом", Emoji: "дом"},
			{Tag: "чужой", Emoji: "🏠"},
		},
	}}}, "текст", 0)

	note := result.Notes[0]
	if note.FolderEmoji != "" {
		t.Errorf("FolderEmoji = %q", note.FolderEmoji)
	}
	if want := []tagEmoji{{Tag: "идея", Emoji: "💡"}}; !slices.Equal(note.TagEmojis, want) {
		t.Errorf("TagEmojis = %v, want %v", note.TagEmojis, want)
	}
}

func TestBuildPromptIncludesContext(t *testing.T) {
	prompt := buildPrompt("запись", "2026-11-06", organizerContext{
		Folders: []string{"Работа"},
		Tags:    []string{"идея"},
		Notes:   []NoteRef{{ID: "1", Title: "Старая заметка"}},
	})
	for _, part := range []string{
		"Сегодня: 2026-11-06, пятница",
		"2026-11-05 — четверг, вчера",
		"2026-11-04 — среда, позавчера",
		"Работа", "идея", "1. Старая заметка",
		"<entry>\nзапись\n</entry>",
	} {
		if !strings.Contains(prompt, part) {
			t.Errorf("prompt lacks %q:\n%s", part, prompt)
		}
	}
}

type failingModel struct{}

func (failingModel) Structured(context.Context, string, string, string, any, any) error {
	return errors.New("offline")
}

func TestOrganizeReportsModelFailure(t *testing.T) {
	if _, err := organize(context.Background(), failingModel{}, "текст", "2026-11-06", organizerContext{}); err == nil {
		t.Fatal("expected an error")
	}
}
