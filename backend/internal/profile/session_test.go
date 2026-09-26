package profile

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

type sessionRepo struct {
	Repository
	capability    Capability
	session       Session
	completed     Evaluation
	clarification []Question
}

func (r *sessionRepo) GetCapability(context.Context, uuid.UUID, uuid.UUID) (Capability, error) {
	return r.capability, nil
}
func (r *sessionRepo) CreateSession(_ context.Context, _ uuid.UUID, value Session) (Session, error) {
	value.ID = uuid.New()
	r.session = value
	return value, nil
}
func (r *sessionRepo) GetSession(_ context.Context, _, _ uuid.UUID) (Session, error) {
	return r.session, nil
}
func (r *sessionRepo) AddClarification(_ context.Context, _, _ uuid.UUID, answers []AnswerInput, questions []Question) (Session, error) {
	r.session.Status = "clarifying"
	r.session.Answers = answers
	r.session.Questions = append(r.session.Questions, questions...)
	r.session.ClarificationCount = len(questions)
	r.clarification = questions
	return r.session, nil
}
func (r *sessionRepo) CompleteSession(_ context.Context, _, _ uuid.UUID, answers []AnswerInput, evaluation Evaluation) (Session, error) {
	r.completed = evaluation
	r.session.Status = "evaluated"
	r.session.Answers = answers
	r.session.Evaluation = &evaluation
	return r.session, nil
}

type sessionAssessor struct {
	Assessor
	evaluation Evaluation
	err        error
}

func (a *sessionAssessor) GenerateQuestions(_ context.Context, _ uuid.UUID, _ Capability, _ PracticeMode, _ int, count int) ([]Question, error) {
	q := make([]Question, count)
	for i := range q {
		q[i] = Question{ID: fmt.Sprintf("q%d", i+1), Prompt: "解释场景", Dimension: "场景", Position: i + 1}
	}
	return q, nil
}
func (a *sessionAssessor) EvaluateAnswers(_ context.Context, _ uuid.UUID, _ Session, _ []AnswerInput, _ []LevelDefinition) (Evaluation, error) {
	return a.evaluation, a.err
}

func TestValidationUsesSixQuestionsAndRequiresConfirmation(t *testing.T) {
	id := uuid.New()
	r := &sessionRepo{capability: Capability{AbilityID: id, Name: "Go", Assessed: true, CurrentLevel: 1, Levels: make([]LevelDefinition, 6)}}
	a := &sessionAssessor{}
	s := NewService(r, a)
	session, err := s.StartSession(context.Background(), uuid.New(), StartSessionInput{AbilityID: id, Mode: ModeValidation})
	if err != nil {
		t.Fatal(err)
	}
	if len(session.Questions) != 6 || session.TargetLevel != 2 {
		t.Fatalf("unexpected validation session: %+v", session)
	}
	answers := make([]AnswerInput, 6)
	results := make([]QuestionResult, 6)
	for i, q := range session.Questions {
		answers[i] = AnswerInput{QuestionID: q.ID, Answer: "有具体回答"}
		results[i] = QuestionResult{QuestionID: q.ID, Passed: i != 0, Feedback: "有待改进"}
	}
	a.evaluation = Evaluation{Verdict: "pass", Passed: true, Score: .5, Summary: "综合达到 L2", QuestionResults: results}
	result, err := s.SubmitSession(context.Background(), uuid.New(), session.ID, answers)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Evaluation.Passed || result.LevelUpdated {
		t.Fatalf("model verdict ignored or auto-upgraded: %+v", result)
	}
}

func TestClarificationAndModelFailureKeepSession(t *testing.T) {
	id := uuid.New()
	r := &sessionRepo{capability: Capability{AbilityID: id, Name: "Go", Assessed: true, CurrentLevel: 1, Levels: make([]LevelDefinition, 6)}}
	a := &sessionAssessor{}
	s := NewService(r, a)
	session, err := s.StartSession(context.Background(), uuid.New(), StartSessionInput{AbilityID: id, Mode: ModeValidation})
	if err != nil {
		t.Fatal(err)
	}
	answers := make([]AnswerInput, 6)
	results := make([]QuestionResult, 6)
	for i, q := range session.Questions {
		answers[i] = AnswerInput{QuestionID: q.ID, Answer: "示例"}
		results[i] = QuestionResult{QuestionID: q.ID, Feedback: "待澄清"}
	}
	a.err = errors.New("model unavailable")
	if _, err := s.SubmitSession(context.Background(), uuid.New(), session.ID, answers); err == nil || r.session.Status != "ready" {
		t.Fatalf("model failure changed session: %v %+v", err, r.session)
	}
	a.err = nil
	a.evaluation = Evaluation{Verdict: "uncertain", Score: .5, Summary: "需要澄清", QuestionResults: results, Followups: []Question{{ID: "f1", Prompt: "请补充关键取舍", Dimension: "取舍", Position: 7}}}
	result, err := s.SubmitSession(context.Background(), uuid.New(), session.ID, answers)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "clarifying" || len(result.Questions) != 7 || result.ClarificationCount != 1 {
		t.Fatalf("followup not persisted: %+v", result)
	}
}
