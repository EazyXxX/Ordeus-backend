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

func TestNormalizePlanLimitsNotes(t *testing.T) {
	raw := plan{}
	for range maxPlannedNotes + 3 {
		raw.Notes = append(raw.Notes, plannedNote{Body: "x"})
	}
	if got := normalizePlan(raw, "x", 0); len(got.Notes) != maxPlannedNotes {
		t.Errorf("notes = %d", len(got.Notes))
	}
}

func TestBuildPromptIncludesContext(t *testing.T) {
	prompt := buildPrompt("запись", organizerContext{
		Folders: []string{"Работа"},
		Tags:    []string{"идея"},
		Notes:   []NoteRef{{ID: "1", Title: "Старая заметка"}},
	})
	for _, part := range []string{"Работа", "идея", "1. Старая заметка", "<entry>\nзапись\n</entry>"} {
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
	if _, err := organize(context.Background(), failingModel{}, "текст", organizerContext{}); err == nil {
		t.Fatal("expected an error")
	}
}
