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
		for _, signal := range ability.IncludeSignals {
			if needle != "" && strings.Contains(normalizedLabel(signal), needle) {
				score = max(score, 8)
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
		out = append(out, abilityMatch(a))
	}
	return out
}

func contextAbilityCandidates(evidence string, abilities []AbilityOption, limit int) []AbilityMatch {
	quote := normalizedLabel(evidence)
	values := []AbilityMatch{}
	for _, a := range abilities {
		for _, name := range append([]string{a.Name}, a.Aliases...) {
			key := normalizedLabel(name)
			if len([]rune(key)) >= 3 && strings.Contains(quote, key) {
				values = append(values, abilityMatch(a))
				break
			}
		}
		if len(values) >= limit {
			break
		}
	}
	return values
}
