package postgres

import "testing"

func TestAnalysisFailureReasonDistinguishesInvalidModelOutput(t *testing.T) {
	invalidResponse := analysisFailureReason("model_invalid_response")
	configuration := analysisFailureReason("model_not_configured")
	if invalidResponse == configuration {
		t.Fatal("invalid model output must not be reported as a model configuration problem")
	}
	if invalidResponse == "" || configuration == "" {
		t.Fatal("failure reasons must be user-readable")
	}
}
