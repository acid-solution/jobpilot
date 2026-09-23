package postgres

import "testing"

func TestClassifyTargetRelationship(t *testing.T) {
	targets := []targetDirection{
		{categoryCode: "backend", specialtyCode: "backend-business"},
		{categoryCode: "ai-application", specialtyCode: "agent-application"},
	}
	tests := []struct {
		name            string
		employmentType  string
		classifications []savedClassification
		wantStatus      string
	}{
		{
			name:           "any primary direction is enough",
			employmentType: "internship",
			classifications: []savedClassification{{
				categoryCode: "backend", specialtyCode: "backend-business", relation: "primary",
			}},
			wantStatus: "included",
		},
		{
			name:           "secondary match is reference only",
			employmentType: "internship",
			classifications: []savedClassification{{
				categoryCode: "ai-application", specialtyCode: "agent-application", relation: "secondary",
			}},
			wantStatus: "reference",
		},
		{
			name:           "no direction match is excluded",
			employmentType: "internship",
			classifications: []savedClassification{{
				categoryCode: "frontend", specialtyCode: "frontend-web", relation: "primary",
			}},
			wantStatus: "excluded",
		},
		{
			name:           "employment type mismatch is excluded",
			employmentType: "campus",
			classifications: []savedClassification{{
				categoryCode: "backend", specialtyCode: "backend-business", relation: "primary",
			}},
			wantStatus: "excluded",
		},
		{
			name:           "unknown employment type is excluded",
			employmentType: "unknown",
			classifications: []savedClassification{{
				categoryCode: "backend", specialtyCode: "backend-business", relation: "primary",
			}},
			wantStatus: "excluded",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, _ := classifyTargetRelationship("internship", test.employmentType, targets, test.classifications)
			if status != test.wantStatus {
				t.Fatalf("status=%q want=%q", status, test.wantStatus)
			}
		})
	}
}
