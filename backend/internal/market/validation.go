package market

import (
	"regexp"
	"strings"
	"unicode"
)

var urlPattern = regexp.MustCompile(`(?i)\b(?:https?://|www\.)\S+`)

// ValidateJDText only rejects obvious garbage before creating a database job.
// Whether meaningful text is actually a JD remains the model and structural
// validator's responsibility.
func ValidateJDText(rawText string) error {
	text := strings.TrimSpace(rawText)
	runes := []rune(text)
	if len(runes) < 20 || len(runes) > 100000 {
		return ErrInvalidJDText
	}

	withoutURLs := urlPattern.ReplaceAllString(text, " ")
	letterCount := 0
	compact := make([]rune, 0, len(runes))
	counts := make(map[rune]int)
	for _, current := range []rune(withoutURLs) {
		if unicode.IsSpace(current) {
			continue
		}
		compact = append(compact, current)
		counts[current]++
		if unicode.IsLetter(current) {
			letterCount++
		}
	}
	if letterCount < 10 || len(compact) == 0 {
		return ErrInvalidJDText
	}

	for _, count := range counts {
		if count*10 >= len(compact)*8 {
			return ErrInvalidJDText
		}
	}
	if isMostlyRepeatedPhrase(compact) {
		return ErrInvalidJDText
	}
	return nil
}

func isMostlyRepeatedPhrase(text []rune) bool {
	for period := 1; period <= 16 && period*4 <= len(text); period++ {
		matches := 0
		for index, current := range text {
			if current == text[index%period] {
				matches++
			}
		}
		if matches*10 >= len(text)*9 {
			return true
		}
	}
	return false
}
