package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilitygrading"
	"github.com/LeoninCS/jobpilot-next/backend/internal/model/deepseek"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/google/uuid"
)

type fixedCredential struct{ key string }

func (c fixedCredential) Credentials(context.Context, uuid.UUID) (modelconfig.Credentials, error) {
	return modelconfig.Credentials{Provider: "deepseek", Model: "deepseek-chat", APIKey: c.key}, nil
}

func TestDeepSeekRealJDOptionGradingLive(t *testing.T) {
	if os.Getenv("JOBPILOT_REAL_MODEL_TEST") != "1" {
		t.Skip("live model test disabled")
	}
	dsn, key := os.Getenv("JOBPILOT_TEST_DATABASE_URL"), os.Getenv("PLATFORM_DEEPSEEK_API_KEY")
	if dsn == "" || key == "" {
		t.Skip("database or key missing")
	}
	db, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var input abilitygrading.Input
	var responsibilities []byte
	err = db.QueryRowContext(ctx, `SELECT jd.id,COALESCE(jd.title,''),jd.responsibilities,jd.raw_text
		FROM job_descriptions jd WHERE jd.status='included' AND jd.validation_status='valid'
		AND EXISTS(SELECT 1 FROM job_description_ability_requirements req JOIN job_description_ability_requirement_options opt ON opt.requirement_id=req.id WHERE req.job_description_id=jd.id AND opt.ability_id IS NOT NULL)
		AND NOT EXISTS(SELECT 1 FROM job_description_ability_requirements req JOIN job_description_ability_requirement_options opt ON opt.requirement_id=req.id WHERE req.job_description_id=jd.id AND opt.resolution_status<>'resolved')
		ORDER BY LENGTH(jd.raw_text) LIMIT 1`).Scan(&input.JobDescriptionID, &input.Title, &responsibilities, &input.RawText)
	if err == sql.ErrNoRows {
		t.Skip("no fully resolved real JD available")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(responsibilities, &input.Responsibilities); err != nil {
		t.Fatal(err)
	}
	input.Abilities, err = NewAbilityGradingRepository(db).loadAbilities(ctx, input.JobDescriptionID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := deepseek.NewClient("https://api.deepseek.com", nil).GradeJDAbilities(ctx, key, "deepseek-chat", input)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateGradingResult(input, result); err != nil {
		t.Fatalf("live option grading invalid: %v", err)
	}
	t.Logf("DeepSeek independently graded %d option(s) for a real JD", len(result.Assessments))
}

// Opt-in live acceptance: uses real, already confirmed material evidence and
// the server-side platform key, without persisting any simulated answers.
func TestDeepSeekKnowledgeInterviewLive(t *testing.T) {
	if os.Getenv("JOBPILOT_REAL_MODEL_TEST") != "1" {
		t.Skip("live model test disabled")
	}
	dsn, key := os.Getenv("JOBPILOT_TEST_DATABASE_URL"), os.Getenv("PLATFORM_DEEPSEEK_API_KEY")
	if dsn == "" || key == "" {
		t.Skip("database or platform key missing")
	}
	db, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var readyMaterials, mostIncluded int
	_ = db.QueryRow(`SELECT COUNT(*) FROM user_profile_materials WHERE status='ready'`).Scan(&readyMaterials)
	_ = db.QueryRow(`SELECT COALESCE(MAX(c),0) FROM (SELECT COUNT(*) c FROM job_descriptions WHERE status='included' AND validation_status='valid' GROUP BY user_id,target_id) grouped`).Scan(&mostIncluded)
	t.Logf("live-data readiness: confirmed materials=%d, max included JDs per goal=%d", readyMaterials, mostIncluded)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var userID, abilityID uuid.UUID
	err = db.QueryRowContext(ctx, `SELECT m.user_id,e.ability_id FROM user_profile_evidence e
		JOIN user_profile_materials m ON m.id=e.material_id AND m.status='ready'
		JOIN abilities a ON a.id=e.ability_id AND a.is_active
		WHERE (SELECT COUNT(*) FROM ability_levels l WHERE l.ability_id=a.id)=6
		AND EXISTS(SELECT 1 FROM job_description_ability_requirement_options o
		  JOIN job_description_ability_requirements req ON req.id=o.requirement_id
		  JOIN job_descriptions jd ON jd.id=req.job_description_id AND jd.status='included'
		  JOIN job_targets target ON target.id=jd.target_id AND target.is_current
		  WHERE jd.user_id=m.user_id AND o.ability_id=e.ability_id)
		LIMIT 1`).Scan(&userID, &abilityID)
	if err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	assessor := profile.NewModelAssessor(fixedCredential{key}, deepseek.NewClient("https://api.deepseek.com", nil))
	var capability profile.Capability
	if err == nil {
		r := NewProfileRepository(db)
		capability, err = r.GetCapability(ctx, userID, abilityID)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		path := os.Getenv("JOBPILOT_REAL_MATERIAL_FILE")
		if path == "" {
			t.Skip("no confirmed material or explicit real material file")
		}
		userID = uuid.New()
		if err := db.QueryRowContext(ctx, `SELECT id,name FROM abilities WHERE name='Go' AND is_active LIMIT 1`).Scan(&abilityID, &capability.Name); err != nil {
			t.Fatal(err)
		}
		capability.AbilityID = abilityID
		rows, err := db.QueryContext(ctx, `SELECT level,description FROM ability_levels WHERE ability_id=$1 ORDER BY level`, abilityID)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var l profile.LevelDefinition
			if err := rows.Scan(&l.Level, &l.Description); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			capability.Levels = append(capability.Levels, l)
		}
		rows.Close()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		title, text, err := profile.ExtractMaterialDocument(path, data)
		if err != nil {
			t.Fatal(err)
		}
		drafts, err := assessor.ExtractEvidence(ctx, userID, profile.Material{Type: profile.MaterialResume, Title: title, Text: text}, []profile.CapabilityInput{{AbilityID: abilityID, Name: capability.Name, Levels: capability.Levels}})
		if err != nil {
			t.Fatal(err)
		}
		for _, draft := range drafts {
			capability.Evidence = append(capability.Evidence, profile.Evidence{AbilityID: abilityID, Level: draft.Level, Quote: draft.Quote, Reason: draft.Reason, Confidence: draft.Confidence})
		}
		t.Logf("DeepSeek extracted %d grounded Go evidence items from real material", len(drafts))
	}
	questions, err := assessor.GenerateQuestions(ctx, userID, capability, profile.ModeValidation, min(capability.CurrentLevel+1, 5), 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(questions) != 6 {
		t.Fatalf("want six core questions, got %d", len(questions))
	}
	answers := make([]profile.AnswerInput, 6)
	for i, q := range questions {
		answers[i] = profile.AnswerInput{QuestionID: q.ID, Answer: "这部分目前不确定，暂时无法给出完整方案。"}
	}
	session := profile.Session{AbilityID: abilityID, AbilityName: capability.Name, Mode: profile.ModeValidation, BaseLevel: capability.CurrentLevel, TargetLevel: min(capability.CurrentLevel+1, 5), Questions: questions}
	evaluation, err := assessor.EvaluateAnswers(ctx, userID, session, answers, capability.Levels)
	if err != nil {
		t.Fatal(err)
	}
	if len(evaluation.QuestionResults) != 6 || evaluation.Summary == "" {
		t.Fatalf("invalid live feedback for six questions")
	}
	t.Logf("DeepSeek returned %d core questions, verdict=%s, followups=%d", len(questions), evaluation.Verdict, len(evaluation.Followups))
}
