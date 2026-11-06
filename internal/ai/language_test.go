package ai

import "testing"

func TestReplyRule(t *testing.T) {
	if ReplyRule("Bought groceries and walked home") == "" {
		t.Error("English text got no rule")
	}
	for _, text := range []string{"Купил продукты и пошёл домой", "Купил iPhone вчера вечером", ""} {
		if ReplyRule(text) != "" {
			t.Errorf("ReplyRule(%q) is not empty", text)
		}
	}
}
