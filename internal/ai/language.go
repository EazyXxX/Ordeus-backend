package ai

import "unicode"

// englishReply goes last in a prompt. The prompts are in Russian, so without it a
// model answers English entries in Russian.
const englishReply = "\n\nЗаписи на английском. Все текстовые поля ответа пиши только на английском языке, без единого русского слова."

// ReplyIn is ReplyRule for when there is no text to judge by, only the
// account's language.
func ReplyIn(language string) string {
	if language == "en" {
		return englishReply
	}
	return ""
}

// ReplyRule tells a model to answer in the language of the text, which matters
// only for text that is mostly in Latin letters.
func ReplyRule(text string) string {
	var latin, cyrillic int
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Latin, r):
			latin++
		case unicode.Is(unicode.Cyrillic, r):
			cyrillic++
		}
	}
	if latin > cyrillic {
		return englishReply
	}
	return ""
}
