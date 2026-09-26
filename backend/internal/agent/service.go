package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/LeoninCS/jobpilot-next/backend/internal/embedding"
	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/LeoninCS/jobpilot-next/backend/internal/mutationlock"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/projectrecs"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

type TargetService interface {
	Current(context.Context, uuid.UUID) (target.Target, error)
	UpsertCurrent(context.Context, uuid.UUID, target.UpsertInput) (target.Target, error)
	Catalog(context.Context) ([]target.Category, error)
}
type MarketService interface {
	Submit(context.Context, uuid.UUID, string) (market.JobDescription, error)
	SubmitBatch(context.Context, uuid.UUID, []string) (market.BatchResult, error)
	ListCurrent(context.Context, uuid.UUID, market.ListFilter) ([]market.JobDescription, error)
	Detail(context.Context, uuid.UUID, uuid.UUID) (market.JobDescription, error)
	Update(context.Context, uuid.UUID, uuid.UUID, string) (market.JobDescription, error)
	Delete(context.Context, uuid.UUID, uuid.UUID) error
	Retry(context.Context, uuid.UUID, uuid.UUID) (market.JobDescription, error)
	RetryAbilityReviews(context.Context, uuid.UUID, uuid.UUID) (market.JobDescription, error)
	RetryAbilityGrading(context.Context, uuid.UUID, uuid.UUID) (market.JobDescription, error)
	ProfileCurrent(context.Context, uuid.UUID) (market.Profile, error)
}
type ProfileService interface {
	GetOverview(context.Context, uuid.UUID) (profile.Overview, error)
	GetSettings(context.Context, uuid.UUID) (profile.Settings, error)
	SaveSettings(context.Context, uuid.UUID, profile.Settings) (profile.Settings, error)
	ListMaterials(context.Context, uuid.UUID) ([]profile.Material, error)
	CreateMaterial(context.Context, uuid.UUID, profile.MaterialInput) (profile.Material, error)
	UpdateMaterial(context.Context, uuid.UUID, uuid.UUID, profile.MaterialInput) (profile.Material, error)
	DeleteMaterial(context.Context, uuid.UUID, uuid.UUID) error
	ConfirmMaterial(context.Context, uuid.UUID, uuid.UUID) (profile.Material, error)
	SetCapabilityLevel(context.Context, uuid.UUID, uuid.UUID, int) (profile.Capability, error)
}
type GapsService interface {
	Get(context.Context, uuid.UUID) (knowledgegaps.View, error)
	Analyze(context.Context, uuid.UUID) (knowledgegaps.View, error)
}
type ProjectsService interface {
	Get(context.Context, uuid.UUID) (projectrecs.View, error)
	Generate(context.Context, uuid.UUID, string) (projectrecs.View, error)
	Select(context.Context, uuid.UUID, uuid.UUID, string) (projectrecs.View, error)
}
type Credentials interface {
	Credentials(context.Context, uuid.UUID) (modelconfig.Credentials, error)
}
type VectorSearch interface {
	SearchSources(context.Context, uuid.UUID, uuid.UUID, []float32, int) ([]SourceMatch, error)
}

// ActionTransactions keeps the lock, snapshot validation, business changes and
// action receipt in one database transaction. A savepoint isolates a rejected
// business operation so its failure receipt can still be committed.
type ActionTransactions interface {
	WithinUserTransaction(context.Context, uuid.UUID, func(context.Context) error) error
	WithinSavepoint(context.Context, func(context.Context) error) error
}
type SourceMatch struct {
	SourceType string    `json:"source_type"`
	SourceID   uuid.UUID `json:"source_id"`
	Start      int       `json:"start_offset"`
	End        int       `json:"end_offset"`
	Quote      string    `json:"quote"`
}

type Service struct {
	Repo             Repository
	Checkpoints      CheckpointStore
	MutationLocker   mutationlock.Locker
	Transactions     ActionTransactions
	Targets          TargetService
	Market           MarketService
	Profile          ProfileService
	Gaps             GapsService
	Projects         ProjectsService
	Credentials      Credentials
	Vectors          VectorSearch
	Embedder         *embedding.Client
	EmbeddingEnabled bool
	DeepSeekBaseURL  string
}

func (s *Service) scope(ctx context.Context, user uuid.UUID) (string, *target.Target, error) {
	v, err := s.Targets.Current(ctx, user)
	if errors.Is(err, target.ErrNotFound) {
		return "no-target", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	directions := make([]string, 0, len(v.Directions))
	for _, d := range v.Directions {
		part := d.CategoryID.String()
		if d.SpecialtyID != nil {
			part += "/" + d.SpecialtyID.String()
		}
		directions = append(directions, part)
	}
	sort.Strings(directions)
	year := 0
	if v.GraduationYear != nil {
		year = *v.GraduationYear
	}
	payload := fmt.Sprintf("%s|%s|%d|%s", v.ID, v.EmploymentType, year, strings.Join(directions, ","))
	h := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(h[:]), &v, nil
}
func (s *Service) List(ctx context.Context, user uuid.UUID) ([]Conversation, error) {
	scope, _, err := s.scope(ctx, user)
	if err != nil {
		return nil, err
	}
	return s.Repo.List(ctx, user, scope)
}
func (s *Service) Get(ctx context.Context, user, id uuid.UUID) (Conversation, error) {
	scope, _, err := s.scope(ctx, user)
	if err != nil {
		return Conversation{}, err
	}
	return s.Repo.Get(ctx, user, scope, id)
}
func (s *Service) Create(ctx context.Context, user uuid.UUID, page string) (Conversation, error) {
	scope, t, err := s.scope(ctx, user)
	if err != nil {
		return Conversation{}, err
	}
	v, err := s.Repo.Create(ctx, user, scope, "新对话")
	if err != nil {
		return v, err
	}
	welcome := s.nextStep(ctx, user, page, t)
	_, err = s.Repo.AddMessage(ctx, v.ID, "assistant", welcome)
	if err != nil {
		return v, err
	}
	return s.Repo.Get(ctx, user, scope, v.ID)
}
func (s *Service) Delete(ctx context.Context, user, id uuid.UUID) error {
	scope, _, err := s.scope(ctx, user)
	if err != nil {
		return err
	}
	return s.Repo.Delete(ctx, user, scope, id)
}
func (s *Service) nextStep(ctx context.Context, user uuid.UUID, page string, t *target.Target) string {
	if t == nil {
		return "下一步：先设置求职目标，然后收集符合目标的岗位 JD。"
	}
	marketView, marketErr := s.Market.ProfileCurrent(ctx, user)
	if marketErr == nil && marketView.IncludedJDCount < 10 {
		return fmt.Sprintf("下一步：当前已计入 %d 份 JD，再补充 %d 份与你的求职目标相符的完整 JD。", marketView.IncludedJDCount, 10-marketView.IncludedJDCount)
	}
	profileView, profileErr := s.Profile.GetOverview(ctx, user)
	if profileErr == nil && !profileView.Complete {
		return fmt.Sprintf("下一步：完善用户画像；目前还有 %d 项能力待评估。", profileView.PendingCount)
	}
	switch page {
	case "projects":
		return "两类画像已具备基础。可以生成项目推荐，再逐项核对推荐理由与参考仓库。"
	case "gaps":
		return "两类画像已具备基础。可以生成知识短板报告，查看完整能力项的等级差距。"
	}
	return "两类画像已具备基础。下一步可查看知识短板，或生成项目推荐。"
}
func hash(value any) string {
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func marshal(value any) json.RawMessage { b, _ := json.Marshal(value); return b }
func compact(value any) string {
	b, _ := json.Marshal(value)
	if len(b) > 24000 {
		preview, _ := json.Marshal(map[string]any{
			"truncated": true,
			"message":   "结果过长，请缩小查询范围后重试。以下仅为预览，不是完整结果。",
			"preview":   string(b[:20000]),
		})
		return string(preview)
	}
	return string(b)
}
