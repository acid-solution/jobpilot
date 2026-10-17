// Package readiness holds the shared admission rules for profile analysis.
package readiness

import (
	"strings"

	"github.com/google/uuid"
)

const RequiredJDCount = 10

type State struct {
	Code                      string `json:"code"`
	Message                   string `json:"message"`
	IncludedJDCount           int    `json:"included_jd_count"`
	PendingReviewCount        int    `json:"pending_review_count"`
	FailedReviewCount         int    `json:"failed_review_count"`
	PendingGradingCount       int    `json:"pending_grading_count"`
	FailedGradingCount        int    `json:"failed_grading_count"`
	MissingAbilityCount       int    `json:"missing_ability_count"`
	MarketComplete            bool   `json:"market_complete"`
	ProfileComplete           bool   `json:"profile_complete"`
	AnalysisReady             bool   `json:"analysis_ready"`
	ReviewsAndGradingComplete bool   `json:"reviews_and_grading_complete"`
}

func NecessaryInformation(hours, weeks *int, experience string) bool {
	return hours != nil && *hours > 0 && weeks != nil && *weeks > 0 && strings.TrimSpace(experience) != ""
}

func ProfileComplete(marketComplete, informationComplete bool, missing int) bool {
	return marketComplete && informationComplete && missing == 0
}

func Evaluate(state State, informationComplete bool) State {
	state.MarketComplete = state.IncludedJDCount >= RequiredJDCount
	state.ProfileComplete = ProfileComplete(state.MarketComplete, informationComplete, state.MissingAbilityCount)
	state.ReviewsAndGradingComplete = state.MarketComplete && state.PendingReviewCount == 0 && state.FailedReviewCount == 0 && state.PendingGradingCount == 0 && state.FailedGradingCount == 0
	state.AnalysisReady = state.ProfileComplete && state.ReviewsAndGradingComplete
	switch {
	case !state.MarketComplete:
		state.Code, state.Message = "market_incomplete", "请先收集至少 10 份计入市场画像的 JD"
	case state.PendingReviewCount > 0 || state.FailedReviewCount > 0:
		state.Code, state.Message = "ability_review_pending", "相关能力目录审核尚未完成，请在 JD 详情中重试失败项"
	case !state.ReviewsAndGradingComplete:
		state.Code, state.Message = "grading_pending", "JD 能力判级尚未完成，请在市场画像中查看或重试"
	case !informationComplete:
		state.Code, state.Message = "profile_incomplete", "请先补齐用户画像的必要信息"
	case state.MissingAbilityCount > 0:
		state.Code, state.Message = "profile_incomplete", "请先评估当前岗位要求所需的能力"
	default:
		state.Code, state.Message = "ready", ""
	}
	return state
}

type Option struct {
	AbilityID                   uuid.UUID
	Resolved, Assessed, Graded  bool
	CurrentLevel, RequiredLevel int
}

// Decidable distinguishes unassessed options from explicit L0. A sufficient
// number of distinct satisfied options makes the remaining paths optional.
func Decidable(required int, options []Option) bool {
	if required < 1 {
		required = 1
	}
	satisfied := map[uuid.UUID]bool{}
	unknown := false
	for _, option := range options {
		if !option.Resolved {
			continue
		}
		if !option.Assessed {
			unknown = true
			continue
		}
		if option.Graded && option.CurrentLevel >= option.RequiredLevel {
			satisfied[option.AbilityID] = true
		}
	}
	return len(satisfied) >= required || !unknown
}
