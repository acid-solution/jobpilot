package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/google/uuid"
)

type readArgs struct {
	Resource string `json:"resource" jsonschema_description:"target,job_catalog,jd_list,jd_detail,market_profile,user_profile,profile_settings,materials,knowledge_gaps,recommendations,source_search,task_status"`
	ID       string `json:"id,omitempty" jsonschema_description:"JD 详情时的 UUID"`
	Query    string `json:"query,omitempty" jsonschema_description:"检索用户原始 JD、简历和经历时的问题"`
}
type writeArgs struct {
	Kind      string          `json:"kind" jsonschema_description:"target_update,jd_add,jd_batch,jd_edit,jd_delete,jd_retry,jd_review_retry,jd_grading_retry,material_add,material_edit,material_delete,material_confirm,profile_settings,capability_level,gaps_analyze,recommendations_generate,project_select"`
	Arguments json.RawMessage `json:"arguments" jsonschema_description:"与现有 JobPilot 业务接口一致的 JSON 对象。旧资源操作需提供 id；项目选择需 report_id 和 project_id。"`
}

var actionTitles = map[string]string{
	"target_update": "更改求职目标", "jd_add": "添加 JD", "jd_batch": "批量添加 JD", "jd_edit": "修改 JD 原文", "jd_delete": "删除 JD", "jd_retry": "重新分析 JD", "jd_review_retry": "重试能力审核", "jd_grading_retry": "重试能力判级",
	"material_add": "添加经历或简历文字", "material_edit": "修改材料", "material_delete": "删除材料", "material_confirm": "确认材料", "profile_settings": "修改画像必要信息", "capability_level": "设置能力等级", "gaps_analyze": "生成知识短板报告", "recommendations_generate": "生成项目推荐", "project_select": "选择项目",
}

func (s *Service) tools(runCtx context.Context, user, conversation uuid.UUID, scope string, t *target.Target, emit func(Event)) ([]tool.BaseTool, error) {
	readTool, err := utils.InferTool("read_jobpilot", "只读查询当前用户有权限访问的 JobPilot 目标、JD、画像、材料、报告、任务与原文引用；需要原文证据时使用 source_search。", func(ctx context.Context, input readArgs) (string, error) {
		emit(Event{Type: "tool", Tool: "read_jobpilot", Text: input.Resource})
		var citations []any
		result, readErr := s.read(runCtx, user, t, input, func(event Event) {
			if event.Type == "citation" && event.Citation != nil {
				citations = append(citations, event.Citation)
			}
			emit(event)
		})
		if readErr != nil {
			return "", readErr
		}
		if len(citations) > 0 {
			record := struct {
				Type      string `json:"type"`
				Citations []any  `json:"citations"`
			}{Type: "source_citations", Citations: citations}
			if _, err := s.Repo.AddMessage(runCtx, conversation, "tool", string(marshal(record))); err != nil {
				return "", err
			}
		}
		return result, nil
	})
	if err != nil {
		return nil, err
	}
	writeTool, err := utils.InferTool("propose_jobpilot_change", "提出一次 JobPilot 业务写操作；首次调用只生成用户可见的待确认动作并暂停。用户确认后恢复此工具，校验资料是否变化，再执行一次写入；取消则不写入。", func(ctx context.Context, input writeArgs) (string, error) {
		wasInterrupted, hasState, state := tool.GetInterruptState[string](ctx)
		if wasInterrupted {
			if !hasState {
				return "", errors.New("missing action checkpoint")
			}
			id, err := uuid.Parse(state)
			if err != nil {
				return "", err
			}
			isTarget, hasDecision, approve := tool.GetResumeContext[bool](ctx)
			if !isTarget {
				return "", tool.StatefulInterrupt(ctx, "等待用户确认", state)
			}
			if !hasDecision {
				return "", errors.New("missing action confirmation decision")
			}
			a, err := s.Repo.GetAction(ctx, user, scope, id)
			if err != nil {
				return "", err
			}
			// Execute only the persisted action whose parameters the user saw.
			// A terminal action is a receipt, never another write on replay.
			if a.Status == "pending" {
				if approve {
					if err := s.executeApprovedAction(ctx, user, conversation, a, emit); err != nil {
						return "", err
					}
				} else {
					if err := s.Repo.CancelAction(ctx, a.ID); err != nil {
						return "", err
					}
					emit(Event{Type: "tool", Tool: a.Kind, Text: "已取消"})
				}
				a, err = s.Repo.GetAction(ctx, user, scope, id)
				if err != nil {
					return "", err
				}
			}
			if a.Status == "succeeded" {
				return "用户已确认，操作成功：" + a.Summary, nil
			}
			if a.Status == "cancelled" {
				return "用户取消了操作，资料未修改。", nil
			}
			return "操作没有完成，状态：" + a.Status, nil
		}
		if _, ok := actionTitles[input.Kind]; !ok {
			return "", fmt.Errorf("unsupported action kind")
		}
		if len(input.Arguments) == 0 || len(input.Arguments) > 100000 || !json.Valid(input.Arguments) {
			return "", fmt.Errorf("invalid action arguments")
		}
		arguments, err := canonicalArguments(input.Kind, input.Arguments)
		if err != nil {
			return "", err
		}
		// Serialize the short snapshot/persistence step with page and background
		// writes. Never hold the lock while waiting for a model or the user.
		if s.MutationLocker != nil {
			release, err := s.MutationLocker.Lock(runCtx, user)
			if err != nil {
				return "", err
			}
			defer release()
		}
		expected, err := s.snapshot(runCtx, user, input.Kind, arguments)
		if err != nil {
			return "", err
		}
		summary := actionTitles[input.Kind]
		var details struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Name  string `json:"name"`
		}
		_ = json.Unmarshal(arguments, &details)
		if details.ID != "" {
			summary += " · " + details.ID
		}
		if details.Title != "" {
			summary += " · " + details.Title
		}
		a, err := s.Repo.CreateAction(runCtx, conversation, user, scope, input.Kind, arguments, summary, expected)
		if err != nil {
			return "", err
		}
		emit(Event{Type: "action", Action: &a})
		return "", tool.StatefulInterrupt(ctx, "等待用户确认", a.ID.String())
	})
	if err != nil {
		return nil, err
	}
	return []tool.BaseTool{readTool, writeTool}, nil
}

// Called only by the resumed write tool after an explicit user approval.
func (s *Service) executeApprovedAction(ctx context.Context, user, conversationID uuid.UUID, a Action, emit func(Event)) error {
	// Direct page mutations use the same account lock in the HTTP middleware.
	// Hold it from the version read through the business write and action receipt.
	if s.MutationLocker != nil {
		release, err := s.MutationLocker.Lock(ctx, user)
		if err != nil {
			return err
		}
		defer release()
	}
	keys := make([]string, 0, len(a.ExpectedVersions))
	for key := range a.ExpectedVersions {
		keys = append(keys, key)
	}
	versions, err := s.Repo.ReadResourceVersions(ctx, user, keys)
	if err != nil {
		return err
	}
	// Check revisions before reading the object: a deleted resource is stale,
	// rather than a missing-object error leaving the action permanently pending.
	if len(a.ExpectedVersions) == 0 || !maps.Equal(versions, a.ExpectedVersions) {
		return s.rejectStaleAction(ctx, a.ID, emit)
	}
	current, err := s.snapshot(ctx, user, a.Kind, a.Arguments)
	if err != nil {
		return err
	}
	if len(a.ExpectedVersions) == 0 || current.Hash != a.ExpectedHash || !maps.Equal(current.Versions, a.ExpectedVersions) {
		return s.rejectStaleAction(ctx, a.ID, emit)
	}
	claimed, err := s.Repo.ClaimAction(ctx, a.ID, conversationID)
	if err != nil {
		return err
	}
	if !claimed {
		return ErrActionClosed
	}
	value, err := s.execute(ctx, user, a.Kind, a.Arguments)
	if err != nil {
		if finishErr := s.Repo.FinishAction(context.WithoutCancel(ctx), a.ID, "failed", nil, "business_operation_failed"); finishErr != nil {
			return finishErr
		}
		emit(Event{Type: "error", Code: "business_operation_failed", Text: err.Error()})
		return nil
	}
	if err := s.Repo.FinishAction(context.WithoutCancel(ctx), a.ID, "succeeded", marshal(value), ""); err != nil {
		return err
	}
	emit(Event{Type: "tool", Tool: a.Kind, Text: "已执行"})
	return nil
}

func (s *Service) rejectStaleAction(ctx context.Context, id uuid.UUID, emit func(Event)) error {
	if err := s.Repo.StaleAction(ctx, id); err != nil {
		return err
	}
	emit(Event{Type: "error", Code: "action_stale", Text: "相关资料已变化，请重新提出操作。"})
	return ErrStale
}

func (s *Service) read(ctx context.Context, user uuid.UUID, t *target.Target, in readArgs, emit func(Event)) (string, error) {
	switch in.Resource {
	case "target":
		if t == nil {
			return "尚未设置求职目标", nil
		}
		return compact(t), nil
	case "job_catalog":
		v, e := s.Targets.Catalog(ctx)
		return compact(v), e
	case "jd_list", "task_status":
		v, e := s.Market.ListCurrent(ctx, user, market.ListFilter{})
		if e != nil {
			return "", e
		}
		return summarizeJDList(v), nil
	case "jd_detail":
		id, e := uuid.Parse(in.ID)
		if e != nil {
			return "", e
		}
		v, e := s.Market.Detail(ctx, user, id)
		return compact(v), e
	case "market_profile":
		v, e := s.Market.ProfileCurrent(ctx, user)
		return compact(v), e
	case "user_profile":
		v, e := s.Profile.GetOverview(ctx, user)
		return compact(v), e
	case "profile_settings":
		v, e := s.Profile.GetSettings(ctx, user)
		return compact(v), e
	case "materials":
		v, e := s.Profile.ListMaterials(ctx, user)
		return compact(v), e
	case "knowledge_gaps":
		v, e := s.Gaps.Get(ctx, user)
		return compact(v), e
	case "recommendations":
		v, e := s.Projects.Get(ctx, user)
		return compact(v), e
	case "source_search":
		if t == nil {
			return "请先设置求职目标", nil
		}
		if !s.EmbeddingEnabled {
			return "原文检索暂不可用：向量功能尚未启用。", nil
		}
		if s.Embedder == nil || !s.Embedder.Configured() || s.Vectors == nil {
			return "原文检索暂不可用：平台 Embedding 未配置。", nil
		}
		query := strings.TrimSpace(in.Query)
		if query == "" || len([]rune(query)) > 500 {
			return "", errors.New("invalid search query")
		}
		vectors, e := s.Embedder.Embed(ctx, []string{query})
		if e != nil {
			return "原文检索暂不可用：Embedding 调用失败。", nil
		}
		matches, e := s.Vectors.SearchSources(ctx, user, t.ID, vectors[0], 5)
		if e != nil {
			return "", e
		}
		for _, match := range matches {
			emit(Event{Type: "citation", Citation: match})
		}
		return compact(matches), nil
	default:
		return "", errors.New("unknown read resource")
	}
}

// A list locates a JD without sending every full source and extracted ability
// through the model context. Exact quotations come from jd_detail or source_search.
func summarizeJDList(jds []market.JobDescription) string {
	type jdSummary struct {
		ID               uuid.UUID     `json:"id"`
		Title            string        `json:"title"`
		Company          string        `json:"company"`
		Status           market.Status `json:"status"`
		JobStatus        string        `json:"job_status"`
		ValidationStatus string        `json:"validation_status"`
	}
	items := make([]jdSummary, 0, len(jds))
	for _, jd := range jds {
		items = append(items, jdSummary{
			ID: jd.ID, Title: jd.Title, Company: jd.Company,
			Status: jd.Status, JobStatus: jd.JobStatus,
			ValidationStatus: string(jd.ValidationStatus),
		})
	}
	return compact(items)
}
func decode[T any](raw json.RawMessage) (T, error) {
	var value T
	err := json.Unmarshal(raw, &value)
	return value, err
}

// Store exactly the fields the business service will consume. The confirmation
// card must never imply that model-supplied but ignored fields will be saved.
func canonicalArguments(kind string, raw json.RawMessage) (json.RawMessage, error) {
	var value any
	switch kind {
	case "target_update":
		v, err := decode[target.UpsertInput](raw)
		if err != nil {
			return nil, err
		}
		value = v
	case "jd_add":
		var v struct {
			RawText string `json:"raw_text"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		value = v
	case "jd_edit":
		var v struct {
			ID      uuid.UUID `json:"id"`
			RawText string    `json:"raw_text"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		value = v
	case "jd_batch":
		var v struct {
			RawTexts []string `json:"raw_texts"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		value = v
	case "jd_delete", "jd_retry", "jd_review_retry", "jd_grading_retry", "material_delete", "material_confirm":
		var v struct {
			ID uuid.UUID `json:"id"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		value = v
	case "material_add":
		v, err := decode[profile.MaterialInput](raw)
		if err != nil {
			return nil, err
		}
		value = v
	case "material_edit":
		var v struct {
			ID    uuid.UUID            `json:"id"`
			Type  profile.MaterialType `json:"type"`
			Title string               `json:"title"`
			Text  string               `json:"text"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		value = v
	case "profile_settings":
		v, err := decode[profile.Settings](raw)
		if err != nil {
			return nil, err
		}
		value = v
	case "capability_level":
		var v struct {
			ID    uuid.UUID `json:"id"`
			Level int       `json:"level"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		value = v
	case "gaps_analyze":
		value = struct{}{}
	case "recommendations_generate":
		var v struct {
			Adjustment string `json:"adjustment"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		value = v
	case "project_select":
		var v struct {
			ReportID  uuid.UUID `json:"report_id"`
			ProjectID string    `json:"project_id"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		value = v
	default:
		return nil, errors.New("unsupported action kind")
	}
	return json.Marshal(value)
}
func parseID(raw json.RawMessage, key string) (uuid.UUID, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return uuid.Nil, err
	}
	var value string
	if err := json.Unmarshal(values[key], &value); err != nil {
		return uuid.Nil, err
	}
	return uuid.Parse(value)
}
func (s *Service) snapshot(ctx context.Context, user uuid.UUID, kind string, args json.RawMessage) (Snapshot, error) {
	_, t, err := s.scope(ctx, user)
	if err != nil {
		return Snapshot{}, err
	}
	keys := []string{"target_selection"}
	if t != nil {
		keys = append(keys, "target:"+t.ID.String())
	}
	switch kind {
	case "jd_edit", "jd_delete", "jd_retry", "jd_review_retry", "jd_grading_retry", "material_edit", "material_delete", "material_confirm", "capability_level":
		id, err := parseID(args, "id")
		if err != nil {
			return Snapshot{}, err
		}
		prefix := "jd:"
		if strings.HasPrefix(kind, "material_") {
			prefix = "material:"
		}
		if kind == "capability_level" {
			prefix = "capability:"
			keys = append(keys, "market", "profile")
		}
		keys = append(keys, prefix+id.String())
	case "profile_settings":
		keys = append(keys, "profile_settings")
	case "gaps_analyze":
		keys = append(keys, "market", "profile", "knowledge_gaps")
	case "recommendations_generate", "project_select":
		keys = append(keys, "market", "profile", "recommendations")
	}
	before, err := s.Repo.ReadResourceVersions(ctx, user, keys)
	if err != nil {
		return Snapshot{}, err
	}
	contentHash, err := s.snapshotHash(ctx, user, kind, args)
	if err != nil {
		return Snapshot{}, err
	}
	after, err := s.Repo.ReadResourceVersions(ctx, user, keys)
	if err != nil {
		return Snapshot{}, err
	}
	if !maps.Equal(before, after) {
		return Snapshot{}, ErrStale
	}
	return Snapshot{Hash: contentHash, Versions: after}, nil
}

func (s *Service) snapshotHash(ctx context.Context, user uuid.UUID, kind string, args json.RawMessage) (string, error) {
	scope, t, err := s.scope(ctx, user)
	if err != nil {
		return "", err
	}
	var resource any = scope
	switch kind {
	case "target_update", "jd_add", "jd_batch":
		resource = t
	case "jd_edit", "jd_delete", "jd_retry", "jd_review_retry", "jd_grading_retry":
		id, e := parseID(args, "id")
		if e != nil {
			return "", e
		}
		resource, err = s.Market.Detail(ctx, user, id)
	case "material_edit", "material_delete", "material_confirm":
		id, e := parseID(args, "id")
		if e != nil {
			return "", e
		}
		items, e := s.Profile.ListMaterials(ctx, user)
		if e != nil {
			return "", e
		}
		found := false
		for _, item := range items {
			if item.ID == id {
				resource = item
				found = true
				break
			}
		}
		if !found {
			return "", ErrNotFound
		}
	case "profile_settings":
		resource, err = s.Profile.GetSettings(ctx, user)
	case "capability_level":
		id, e := parseID(args, "id")
		if e != nil {
			return "", e
		}
		overview, e := s.Profile.GetOverview(ctx, user)
		if e != nil {
			return "", e
		}
		found := false
		for _, cap := range overview.Capabilities {
			if cap.AbilityID == id {
				resource = cap
				found = true
				break
			}
		}
		if !found {
			return "", ErrNotFound
		}
	case "gaps_analyze":
		resource, err = s.Gaps.Get(ctx, user)
	case "recommendations_generate", "project_select":
		resource, err = s.Projects.Get(ctx, user)
	case "material_add":
		resource = t
	default:
		return "", errors.New("unsupported action")
	}
	if err != nil {
		return "", err
	}
	return hash(struct {
		Scope    string
		Resource any
	}{scope, resource}), nil
}
func (s *Service) execute(ctx context.Context, user uuid.UUID, kind string, args json.RawMessage) (any, error) {
	switch kind {
	case "target_update":
		in, e := decode[target.UpsertInput](args)
		if e != nil {
			return nil, e
		}
		return s.Targets.UpsertCurrent(ctx, user, in)
	case "jd_add":
		var in struct {
			RawText string `json:"raw_text"`
		}
		if e := json.Unmarshal(args, &in); e != nil {
			return nil, e
		}
		return s.Market.Submit(ctx, user, in.RawText)
	case "jd_batch":
		var in struct {
			RawTexts []string `json:"raw_texts"`
		}
		if e := json.Unmarshal(args, &in); e != nil {
			return nil, e
		}
		return s.Market.SubmitBatch(ctx, user, in.RawTexts)
	case "jd_edit":
		var in struct {
			ID      uuid.UUID `json:"id"`
			RawText string    `json:"raw_text"`
		}
		if e := json.Unmarshal(args, &in); e != nil {
			return nil, e
		}
		return s.Market.Update(ctx, user, in.ID, in.RawText)
	case "jd_delete":
		id, e := parseID(args, "id")
		if e != nil {
			return nil, e
		}
		return map[string]any{"deleted_id": id}, s.Market.Delete(ctx, user, id)
	case "jd_retry", "jd_review_retry", "jd_grading_retry":
		id, e := parseID(args, "id")
		if e != nil {
			return nil, e
		}
		switch kind {
		case "jd_retry":
			return s.Market.Retry(ctx, user, id)
		case "jd_review_retry":
			return s.Market.RetryAbilityReviews(ctx, user, id)
		default:
			return s.Market.RetryAbilityGrading(ctx, user, id)
		}
	case "material_add":
		in, e := decode[profile.MaterialInput](args)
		if e != nil {
			return nil, e
		}
		return s.Profile.CreateMaterial(ctx, user, in)
	case "material_edit":
		var in struct {
			ID    uuid.UUID            `json:"id"`
			Type  profile.MaterialType `json:"type"`
			Title string               `json:"title"`
			Text  string               `json:"text"`
		}
		if e := json.Unmarshal(args, &in); e != nil {
			return nil, e
		}
		return s.Profile.UpdateMaterial(ctx, user, in.ID, profile.MaterialInput{Type: in.Type, Title: in.Title, Text: in.Text})
	case "material_delete":
		id, e := parseID(args, "id")
		if e != nil {
			return nil, e
		}
		return map[string]any{"deleted_id": id}, s.Profile.DeleteMaterial(ctx, user, id)
	case "material_confirm":
		id, e := parseID(args, "id")
		if e != nil {
			return nil, e
		}
		return s.Profile.ConfirmMaterial(ctx, user, id)
	case "profile_settings":
		in, e := decode[profile.Settings](args)
		if e != nil {
			return nil, e
		}
		return s.Profile.SaveSettings(ctx, user, in)
	case "capability_level":
		var in struct {
			ID    uuid.UUID `json:"id"`
			Level int       `json:"level"`
		}
		if e := json.Unmarshal(args, &in); e != nil {
			return nil, e
		}
		return s.Profile.SetCapabilityLevel(ctx, user, in.ID, in.Level)
	case "gaps_analyze":
		return s.Gaps.Analyze(ctx, user)
	case "recommendations_generate":
		var in struct {
			Adjustment string `json:"adjustment"`
		}
		if e := json.Unmarshal(args, &in); e != nil {
			return nil, e
		}
		return s.Projects.Generate(ctx, user, in.Adjustment)
	case "project_select":
		var in struct {
			ReportID  uuid.UUID `json:"report_id"`
			ProjectID string    `json:"project_id"`
		}
		if e := json.Unmarshal(args, &in); e != nil {
			return nil, e
		}
		return s.Projects.Select(ctx, user, in.ReportID, in.ProjectID)
	default:
		return nil, errors.New("unsupported action")
	}
}
