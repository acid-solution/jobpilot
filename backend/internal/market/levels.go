package market

import "sort"

func BuildLevelSummary(evidences []LevelEvidence, status string) LevelSummary {
	result := LevelSummary{
		CommonLevels: []int{}, Distribution: []LevelDistribution{}, Evidences: evidences, Status: status,
	}
	if len(evidences) == 0 {
		return result
	}
	counts := make(map[int]int)
	for _, evidence := range evidences {
		counts[evidence.Level]++
		if evidence.Source == "explicit" {
			result.ExplicitCount++
		} else if evidence.Source == "inferred" {
			result.InferredCount++
		}
	}
	result.SampleCount = len(evidences)
	maximum := 0
	for level := 1; level <= 5; level++ {
		count := counts[level]
		if count == 0 {
			continue
		}
		result.Distribution = append(result.Distribution, LevelDistribution{Level: level, Count: count})
		if count > maximum {
			maximum = count
			result.CommonLevels = []int{level}
		} else if count == maximum {
			result.CommonLevels = append(result.CommonLevels, level)
		}
	}
	sort.Ints(result.CommonLevels)
	if len(result.CommonLevels) > 0 {
		result.RecommendedLevel = result.CommonLevels[len(result.CommonLevels)-1]
		for level, count := range counts {
			if level > result.RecommendedLevel {
				result.HigherRequirementCount += count
			}
		}
	}
	if result.Status == "" {
		result.Status = "ready"
	}
	return result
}
