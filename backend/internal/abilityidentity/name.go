package abilityidentity

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// NormalizeName is shared by catalog matching, vector normalization and review
// deduplication. Keep meaningful language symbols such as '+' and '#'.
func NormalizeName(value string) string {
	value = strings.ToLower(norm.NFKC.String(strings.TrimSpace(value)))
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune("-_.:/\\·", r) {
			return -1
		}
		return r
	}, value)
}
