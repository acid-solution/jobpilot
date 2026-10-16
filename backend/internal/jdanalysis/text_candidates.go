package jdanalysis

import (
	"sort"
	"strings"
)

// Recall never confirms identity. The model must still choose a matching
// candidate or request independent review; a weak score cannot force a match.
func textAbilityCandidates(label string, abilities []AbilityOption, limit int) []AbilityMatch {
	type scored struct {
		ability AbilityOption
		score   int
	}
	values := []scored{}
	needle := normalizedLabel(label)
	for _, ability := range abilities {
		score := 0
		for _, name := range append([]string{ability.Name}, ability.Aliases...) {
			candidate := normalizedLabel(name)
			if candidate == needle {
				score = max(score, 100)
			} else if candidate != "" && (strings.Contains(needle, candidate) || strings.Contains(candidate, needle)) {
				score = max(score, 20)
			} else {
				letters := []rune(needle)
				for i := 0; i+1 < len(letters); i++ {
					if strings.Contains(candidate, string(letters[i:i+2])) {
						score++
					}
				}
			}
		}
		if score > 0 {
			values = append(values, scored{ability, score})
		}
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].score > values[j].score })
	if len(values) > limit {
		values = values[:limit]
	}
	out := []AbilityMatch{}
	for _, item := range values {
		a := item.ability
		out = append(out, AbilityMatch{Code: a.Code, Name: a.Name, CategoryCode: a.CategoryCode, Aliases: a.Aliases, Definition: a.Definition})
	}
	return out
}
