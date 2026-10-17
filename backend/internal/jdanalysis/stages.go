package jdanalysis

import (
	"context"
	"errors"
	"fmt"

	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelusage"
)

type StageRepository interface {
	LoadStage(context.Context, Job, string) (Result, bool, error)
	SaveStage(context.Context, Job, string, string, Result, string, ...bool) error
}

type ExtractionCheckpoints struct {
	Load func(int) (Result, bool, error)
	Save func(int, Result) error
}
type extractionCheckpointsKey struct{}

func WithExtractionCheckpoints(ctx context.Context, value ExtractionCheckpoints) context.Context {
	return context.WithValue(ctx, extractionCheckpointsKey{}, value)
}
func ExtractionCheckpointsFrom(ctx context.Context) (ExtractionCheckpoints, bool) {
	value, ok := ctx.Value(extractionCheckpointsKey{}).(ExtractionCheckpoints)
	return value, ok
}

func (w *Worker) analyze(ctx context.Context, job Job, creds modelconfig.Credentials, catalog Catalog) (Result, error) {
	analyzer, repo := w.analyzer, w.repository
	run := func(stage string, fn func() (Result, error)) (Result, error) {
		cached, found, err := repo.LoadStage(ctx, job, stage)
		if err != nil || found {
			return cached, err
		}
		if err = repo.SaveStage(ctx, job, stage, "running", Result{}, ""); err != nil {
			return Result{}, err
		}
		value, err := fn()
		if err != nil {
			code, retryable := classify(err)
			if saveErr := repo.SaveStage(ctx, job, stage, "failed", value, code, retryable); saveErr != nil {
				return value, errors.Join(err, saveErr)
			}
			return value, err
		}
		return value, repo.SaveStage(ctx, job, stage, "succeeded", value, "")
	}
	base, err := run("extraction", func() (Result, error) {
		checkpointCtx := WithExtractionCheckpoints(modelusage.For(ctx, job.UserID, job.ID, "jd_extraction", "user"), ExtractionCheckpoints{
			Load: func(index int) (Result, bool, error) {
				return repo.LoadStage(ctx, job, fmt.Sprintf("extraction_window_%d", index))
			},
			Save: func(index int, result Result) error {
				return repo.SaveStage(ctx, job, fmt.Sprintf("extraction_window_%d", index), "succeeded", result, "")
			},
		})
		return analyzer.ExtractJD(checkpointCtx, creds.APIKey, creds.Model, job.RawText)
	})
	if err != nil || base.ValidationStatus != ValidationValid {
		return base, err
	}
	classified, classErr := run("classification", func() (Result, error) {
		out := base
		classes, e := analyzer.ClassifyJD(modelusage.For(ctx, job.UserID, job.ID, "jd_classification", "user"), creds.APIKey, creds.Model, job.RawText, base, catalog)
		out.Classifications = classes
		return out, e
	})
	if classErr == nil {
		base.Classifications = classified.Classifications
		hasCandidate := false
		for _, item := range base.Classifications {
			hasCandidate = hasCandidate || item.Candidate != nil
		}
		if hasCandidate {
			reviewed, e := run("classification_review", func() (Result, error) {
				out := base
				if !w.classificationReview.Enabled || w.classificationReview.Reviewer == nil || w.classificationReview.APIKey == "" {
					return out, NewError("classification_review_not_configured", false, errors.New("岗位新增分类审核服务未配置"))
				}
				value, e := w.classificationReview.Reviewer.ReviewJobClassifications(modelusage.For(ctx, job.UserID, job.ID, "jd_classification_review", "platform"), w.classificationReview.APIKey, w.classificationReview.Model, job.RawText, ClassificationReviewInput{Title: base.Title, Responsibilities: base.Responsibilities, Classifications: base.Classifications, Catalog: catalog.JobCategories})
				if e != nil {
					return out, e
				}
				out.ClassificationReview = &value
				out.Classifications = value.Classifications
				if value.Decision == "reject" || len(value.Classifications) == 0 {
					return out, NewError("classification_rejected", false, errors.New("岗位分类无法匹配已有目录且新增审核未通过"))
				}
				return out, nil
			})
			classErr = e
			if e == nil {
				base.Classifications = reviewed.Classifications
				base.ClassificationReview = reviewed.ClassificationReview
			}
		}
	}
	// Classification and ability resolution checkpoint independently. A failed
	// classification does not prevent successful ability work from being saved.
	normalized, normErr := run("ability_resolution", func() (Result, error) {
		out := base
		if len(out.AbilityRequirements) == 0 {
			out.VectorNormalized = true
			return out, nil
		}
		if w.normalizer == nil {
			return out, NewError("ability_normalization_unavailable", false, errors.New("能力归一化服务未配置"))
		}
		return w.normalizer.Normalize(modelusage.For(ctx, job.UserID, job.ID, "jd_ability_resolution", "user"), job.UserID, creds.APIKey, creds.Model, catalog, out)
	})
	if normErr == nil {
		base.AbilityRequirements = normalized.AbilityRequirements
		base.AbilityMentions = normalized.AbilityMentions
		base.VectorNormalized = normalized.VectorNormalized
		base.AliasProposals = normalized.AliasProposals
	}
	combined := errors.Join(classErr, normErr)
	if combined != nil {
		classCode, classRetry := classify(classErr)
		normCode, normRetry := classify(normErr)
		if normErr != nil && normRetry {
			return base, NewError(normCode, true, combined)
		}
		if classErr != nil && classRetry {
			return base, NewError(classCode, true, combined)
		}
	}
	return base, combined
}
