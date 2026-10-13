package agent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/LeoninCS/jobpilot-next/backend/internal/learningroutes"
	"github.com/google/uuid"
)

type sourceAccessKey struct{}

// Transient server context only: never arguments, messages or checkpoints.
func WithSourceAccess(ctx context.Context, raw string) context.Context {
	return context.WithValue(ctx, sourceAccessKey{}, raw)
}
func sourceAccess(ctx context.Context) string {
	raw, _ := ctx.Value(sourceAccessKey{}).(string)
	return raw
}
func (s *Service) learningGuard(ctx context.Context, user uuid.UUID, kind string, args json.RawMessage) (any, error) {
	if s.Learning == nil {
		return nil, errors.New("学习路线尚未启用")
	}
	if kind == "route_import_retry" {
		id, e := parseID(args, "id")
		if e != nil {
			return nil, e
		}
		r, e := s.Learning.GetImport(ctx, user, id)
		if e != nil {
			return nil, e
		}
		return struct {
			ID     uuid.UUID
			Digest string
		}{r.ID, r.Digest}, nil
	}
	v, e := s.Learning.Get(ctx, user)
	if e != nil {
		return nil, e
	}
	return struct {
		Version   *learningroutes.Version
		Readiness any
		Knowledge any
	}{v.Version, v.Readiness, v.Knowledge}, nil
}
func (s *Service) finishExternalRoute(ctx context.Context, user uuid.UUID, a Action, r learningroutes.ImportRequest, emit func(Event)) error {
	err := s.Learning.Dispatch(ctx, user, r.ID, sourceAccess(ctx))
	current, readErr := s.Learning.GetImport(ctx, user, r.ID)
	if err == nil && readErr == nil && current.Status == "succeeded" && current.Receipt != nil {
		if e := s.Repo.FinishAction(ctx, a.ID, "succeeded", marshal(current.Receipt), ""); e != nil {
			return e
		}
		emit(Event{Type: "tool", Tool: a.Kind, Text: "已导入 StudyFlow 草稿，请前往激活"})
		return nil
	}
	code, text := "external_result_unknown", "导入请求已确认，结果待核实。请在学习路线页面使用同一请求查询或重试。"
	if readErr == nil && current.Status == "failed" {
		code, text = "studyflow_import_rejected", "StudyFlow 未接受导入，请先激活或取消已有草稿，再重试同一请求。"
	}
	if e := s.Repo.FinishAction(context.WithoutCancel(ctx), a.ID, "failed", marshal(map[string]any{"import_request_id": r.ID}), code); e != nil {
		return e
	}
	emit(Event{Type: "error", Code: code, Text: text})
	return nil
}
