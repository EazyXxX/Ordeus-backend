package diary

import (
	"ordeus/backend/internal/ai"

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

func TestWithLocalFallback(t *testing.T) {
	notes := []plannedNote{{Folder: fallbackFolder}, {Folder: "Работа"}}
	if got := withLocalFallback(notes, "en"); got[0].Folder != englishFallback || got[1].Folder != "Работа" {
		t.Errorf("notes = %+v", got)
	}
	if got := withLocalFallback(notes, "ru"); got[0].Folder != fallbackFolder {
		t.Errorf("notes = %+v", got)
	}
}

func TestValidWeek(t *testing.T) {
	if !validWeek("2026-11-02") {
		t.Error("monday rejected")
	}
	for _, week := range []string{"2026-11-03", "2026-13-02", "", "понедельник"} {
		if validWeek(week) {
			t.Errorf("validWeek(%q) = true", week)
		}
	}
}

func TestNormalizeSummary(t *testing.T) {
	got, err := normalizeSummary(summaryPlan{
		Headline:   " Неделя ",
		Text:       " Текст ",
		Highlights: []string{" раз ", "", "два", "три", "четыре", "пять", "шесть"},
		Themes:     []string{" Спорт "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Headline != "Неделя" || got.Text != "Текст" || len(got.Highlights) != maxHighlights {
		t.Errorf("summary = %+v", got)
	}
	if got.Themes[0] != "спорт" {
		t.Errorf("themes = %v", got.Themes)
	}
	if _, err := normalizeSummary(summaryPlan{Headline: "Без текста"}); err == nil {
		t.Error("empty summary accepted")
	}
}

func TestBuildSummaryPromptFitsTheWeekIntoTheBudget(t *testing.T) {
	long := strings.Repeat("слово ", 3000)
	notes := make([]weekNote, 200)
	for i := range notes {
		notes[i] = weekNote{Day: "2026-11-02", Title: "Запись", Body: long}
	}
	notes[199].Title = "Последняя"

	prompt := buildSummaryPrompt("2026-11-02", notes, nil)
	if runes := len([]rune(prompt)); runes > summaryPromptRunes*2 {
		t.Errorf("prompt is %d runes long", runes)
	}
	if !strings.Contains(prompt, "<entries>") || !strings.HasSuffix(prompt, "</entries>") {
		t.Errorf("prompt lacks entries: %.80s", prompt)
	}
}

func TestValidMoods(t *testing.T) {
	if !validMoods(nil) || !validMoods([]string{"apathy", "swings"}) {
		t.Error("valid moods rejected")
	}
	if validMoods([]string{"joy", "joy"}) || validMoods([]string{"восторг"}) {
		t.Error("invalid moods accepted")
	}
}

func TestSummaryPromptsCarryDayMarks(t *testing.T) {
	score := 8
	day := Day{Date: "2026-11-02", Moods: []string{"apathy", "obsessed"}, Score: &score}
	for _, prompt := range []string{
		buildSummaryPrompt("2026-11-02", nil, []Day{day}),
		buildDayPrompt(day, []weekNote{{Title: "Запись", Body: "Текст"}}),
	} {
		if !strings.Contains(prompt, "апатия, одержимость чем-то, оценка дня 8 из 10") {
			t.Errorf("prompt lacks the day mark:\n%s", prompt)
		}
	}
	if mark := dayMark(Day{}); mark != "" {
		t.Errorf("dayMark = %q", mark)
	}
}

func TestBuildPromptIncludesContext(t *testing.T) {
	prompt := buildPrompt("запись", "2026-11-06", organizerContext{
		Folders: []string{"Работа"},
		Tags:    []string{"идея"},
		Notes:   []NoteRef{{ID: "1", Title: "Старая заметка"}},
	}, 0, 0)
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
	if _, err := organize(context.Background(), failingModel{}, "текст", "2026-11-06", organizerContext{}, nil, 0); err == nil {
		t.Fatal("expected an error")
	}
}

func TestWithDatedTitles(t *testing.T) {
	notes := []plannedNote{
		{Title: "Поездка к родителям", DailyDate: "2026-11-08"},
		{Title: "08.11.2026: Поездка", DailyDate: "2026-11-08"},
		{Title: "08.11.20266 идей", DailyDate: "2026-11-08"},
		{Title: "Идеи", DailyDate: ""},
		{Title: "", DailyDate: "2026-03-01"},
	}
	want := []string{
		"08.11.2026 — Поездка к родителям",
		"08.11.2026 — Поездка",
		"08.11.2026 — 08.11.20266 идей",
		"Идеи",
		"01.03.2026",
	}
	for i, note := range withDatedTitles(notes) {
		if note.Title != want[i] {
			t.Errorf("note %d: title = %q, want %q", i, note.Title, want[i])
		}
	}
}

func TestBuildPromptMentionsPictures(t *testing.T) {
	prompt := buildPrompt("", "2026-11-06", organizerContext{Language: "en"}, 3, 5)
	if !strings.Contains(prompt, "приложено фотографий: 5. Тебе показаны первые 3") {
		t.Errorf("prompt lacks the pictures:\n%s", prompt)
	}
	if !strings.Contains(prompt, "на английском") {
		t.Errorf("an English account without text gets no English rule:\n%s", prompt)
	}
	if strings.Contains(buildPrompt("запись", "2026-11-06", organizerContext{}, 0, 0), "фотографий") {
		t.Error("a capture without pictures mentions them")
	}
}

// visionModel records the pictures it was shown and answers with one note.
type visionModel struct {
	seen int
}

func (m *visionModel) Structured(ctx context.Context, system, user, name string, schema, target any) error {
	return m.StructuredImages(ctx, system, user, name, schema, target, nil)
}

func (m *visionModel) StructuredImages(_ context.Context, _, _, _ string, _, target any, images []ai.Image) error {
	m.seen = len(images)
	result := target.(*plan)
	result.Notes = []plannedNote{{Title: "", Body: "Закат над рекой.", Folder: "Фото"}}
	return nil
}

func TestOrganizeShowsPicturesAndKeepsTheirDescription(t *testing.T) {
	model := &visionModel{}
	images := []ai.Image{{ContentType: "image/jpeg"}, {ContentType: "image/png"}}
	got, err := organize(context.Background(), model, "  ", "2026-11-06", organizerContext{}, images, 4)
	if err != nil {
		t.Fatal(err)
	}
	if model.seen != 2 {
		t.Errorf("model saw %d pictures", model.seen)
	}
	if got.Notes[0].Body != "Закат над рекой." {
		t.Errorf("note = %+v", got.Notes[0])
	}
}

func TestWithPhotoTitles(t *testing.T) {
	notes := withPhotoTitles([]plannedNote{{Title: " "}, {Title: "Закат"}}, "en")
	if notes[0].Title != "Photo" || notes[1].Title != "Закат" {
		t.Errorf("titles = %q, %q", notes[0].Title, notes[1].Title)
	}
}

func TestNormalizePlanMergesNotesMadeFromPicturesAlone(t *testing.T) {
	raw := plan{Notes: []plannedNote{
		{Title: "Закат", Body: "Закат над рекой.", Tags: []string{"закат"}, RelatedNew: []int{1}},
		{Title: "Кот", Body: "Рыжий кот.", Tags: []string{"коты", "закат"}},
	}}
	got := normalizePlan(raw, "", 0)
	if len(got.Notes) != 1 {
		t.Fatalf("notes = %d", len(got.Notes))
	}
	note := got.Notes[0]
	if note.Title != "Закат" || note.Body != "Закат над рекой.\n\nРыжий кот." || len(note.Tags) != 2 || len(note.RelatedNew) != 0 {
		t.Errorf("note = %+v", note)
	}
}
