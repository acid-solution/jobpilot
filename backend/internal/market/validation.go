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
	if len(runes) == 0 || len(runes) > 100000 {
		return ErrInvalidJDText
	}

	withoutURLs := urlPattern.ReplaceAllString(text, " ")
	letterCount := 0
	compact := make([]rune, 0, len(runes))
	for _, current := range []rune(withoutURLs) {
		if unicode.IsSpace(current) {
			continue
		}
		compact = append(compact, current)
		if unicode.IsLetter(current) {
			letterCount++
		}
	}
	if letterCount == 0 || len(compact) == 0 {
		return ErrInvalidJDText
	}

	// Repeated page furniture must not hide a valid JD elsewhere in the text.
	// Semantic rejection happens after scanning all source windows.
	return nil
}
