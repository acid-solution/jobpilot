package postgres

import (
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilitygrading"
)

func TestNormalizeAssessmentKindsDoesNotBorrowPreferredSignalFromPreviousRequirement(t *testing.T) {
	quote := "2、精通至少一门编程语言，包括 Java、C++、Go"
	input := abilitygrading.Input{
		RawText: "1、本科及以上学历，计算机相关专业优先；\n" + quote,
		Abilities: []abilitygrading.Ability{{
			Code: "ability-go", Evidences: []abilitygrading.Evidence{{RequirementKind: "unspecified", Operator: "any_of", Quote: quote}},
		}},
	}
	result := normalizeAssessmentKinds(input, []abilitygrading.Assessment{{AbilityCode: "ability-go", EvidenceQuote: quote, RequirementKind: "preferred"}})
	if result[0].RequirementKind != "unspecified" {
		t.Fatalf("kind=%q want unspecified", result[0].RequirementKind)
	}
}

func TestNormalizeAssessmentKindsRecognizesBonusHeading(t *testing.T) {
	quote := "用过 LangChain、AutoGen 任一框架"
	input := abilitygrading.Input{
		RawText: "以下至少满足一项（加分项）：\n1）" + quote,
		Abilities: []abilitygrading.Ability{{
			Code: "ability-langchain", Evidences: []abilitygrading.Evidence{{RequirementKind: "unspecified", Operator: "any_of", Quote: quote}},
		}},
	}
	result := normalizeAssessmentKinds(input, []abilitygrading.Assessment{{AbilityCode: "ability-langchain", EvidenceQuote: quote, RequirementKind: "unspecified"}})
	if result[0].RequirementKind != "preferred" {
		t.Fatalf("kind=%q want preferred", result[0].RequirementKind)
	}
}
