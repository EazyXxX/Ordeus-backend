package diary

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// answerModel answers every part with what reply makes of it.
type answerModel struct {
	reply func(text string) string
	calls int
}

func (m *answerModel) Structured(_ context.Context, _, user, _ string, _, target any) error {
	m.calls++
	text, _, _ := strings.Cut(strings.TrimPrefix(user, "<text>\n"), "\n</text>")
	answer, err := json.Marshal(map[string]string{"text": m.reply(text)})
	if err != nil {
		return err
	}
	return json.Unmarshal(answer, target)
}

func TestSplitTextJoinsBackExactly(t *testing.T) {
	line := strings.Repeat("слово ", 100) + "\n\n"
	for _, text := range []string{"", "коротко", strings.Repeat(line, 20), strings.Repeat("я", 7000)} {
		parts := splitText(text, proofreadChunkRunes)
		if joined := strings.Join(parts, ""); joined != text {
			t.Errorf("parts of %d bytes joined into %d bytes", len(text), len(joined))
		}
		for _, part := range parts {
			if runes := len([]rune(part)); runes > proofreadChunkRunes {
				t.Errorf("part has %d runes", runes)
			}
		}
	}
}

func TestSplitTextPrefersLineEnds(t *testing.T) {
	first := strings.Repeat("а", 1000) + "\n"
	parts := splitText(first+strings.Repeat("б ", 1500), proofreadChunkRunes)
	if parts[0] != first {
		t.Errorf("first part has %d runes", len([]rune(parts[0])))
	}
}

func TestProofreadKeepsSurroundingWhitespace(t *testing.T) {
	model := &answerModel{reply: func(string) string { return " Привет. " }}
	got, err := proofread(context.Background(), model, "\n привет\n\n", maxBodyLength)
	if err != nil {
		t.Fatal(err)
	}
	if got != "\n Привет.\n\n" {
		t.Errorf("got %q", got)
	}
}

func TestProofreadSpendsNothingOnBlankText(t *testing.T) {
	model := &answerModel{reply: func(text string) string { return text }}
	if got, err := proofread(context.Background(), model, " \n", maxTitleLength); err != nil || got != " \n" {
		t.Errorf("got %q, %v", got, err)
	}
	if model.calls != 0 {
		t.Errorf("model was called %d times", model.calls)
	}
}

func TestProofreadCallsModelForEachPart(t *testing.T) {
	model := &answerModel{reply: func(text string) string { return text }}
	text := strings.Repeat("Одно предложение.\n", 500)
	got, err := proofread(context.Background(), model, text, maxBodyLength)
	if err != nil {
		t.Fatal(err)
	}
	if got != text {
		t.Error("text changed")
	}
	if model.calls < 2 {
		t.Errorf("model was called %d times", model.calls)
	}
}

func TestProofreadKeepsTextTheModelRewrote(t *testing.T) {
	text := strings.Repeat("Длинная запись автора о его дне. ", 20)
	for name, answer := range map[string]string{
		"retold":          "День прошёл хорошо.",
		"lost a sentence": strings.Repeat("Длинная запись автора о его дне. ", 18),
		"inflated":        strings.Repeat(text, 3),
		"empty":           "",
	} {
		t.Run(name, func(t *testing.T) {
			model := &answerModel{reply: func(string) string { return answer }}
			got, err := proofread(context.Background(), model, text, maxBodyLength)
			if err != nil {
				t.Fatal(err)
			}
			if got != text {
				t.Errorf("got %q", got)
			}
		})
	}
}

func TestProofreadKeepsTextOverTheLimit(t *testing.T) {
	text := strings.Repeat("я", maxTitleLength)
	model := &answerModel{reply: func(string) string { return text + "." }}
	got, err := proofread(context.Background(), model, text, maxTitleLength)
	if err != nil {
		t.Fatal(err)
	}
	if got != text {
		t.Error("expected the original")
	}
}

func TestProofreadReportsModelFailure(t *testing.T) {
	if _, err := proofread(context.Background(), failingModel{}, "текст", maxBodyLength); err == nil {
		t.Fatal("expected an error")
	}
}
