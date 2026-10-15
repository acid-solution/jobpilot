package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/agent"
	"github.com/LeoninCS/jobpilot-next/backend/internal/agentcontext"
	"github.com/LeoninCS/jobpilot-next/backend/internal/assistantmemory"
	"github.com/LeoninCS/jobpilot-next/backend/internal/httpapi"
	"github.com/LeoninCS/jobpilot-next/backend/internal/identity"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

// Opt-in disposable browser fixture. It mounts only AgentWidget, using real
// memory APIs on an isolated test database; it never bypasses a real login.
func TestAgentMemoryBrowserFixture(t *testing.T) {
	if os.Getenv("RUN_AGENT_MEMORY_BROWSER_TEST") != "1" {
		t.Skip("manual browser acceptance fixture")
	}
	root, e := filepath.Abs(os.Getenv("AGENT_MEMORY_BROWSER_ROOT"))
	if e != nil || !strings.HasSuffix(filepath.ToSlash(root), "/frontend/.cache") {
		t.Fatal("fixture directory must be frontend/.cache")
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	db := agentTransactionDatabase(t)
	ctx := context.Background()
	user := uuid.New()
	targets := target.NewService(NewTargetRepository(db))
	repo := NewAgentRepository(db)
	mem := NewAssistantMemoryRepository(db)
	contexts := NewAgentContextRepository(db)
	s := &agent.Service{Repo: repo, Memories: mem, ContextRepo: contexts, Transactions: repo, ContextConfig: agentcontext.DefaultConfig(), Targets: targets}
	conversation, e := repo.Create(ctx, user, "no-target", "记忆验收（合成数据）")
	if e != nil {
		t.Fatal(e)
	}
	token, e := repo.BeginRun(ctx, user, "no-target", conversation.ID)
	if e != nil {
		t.Fatal(e)
	}
	snap, _ := mem.Snapshot(ctx, user, "no-target")
	turn, e := contexts.Start(ctx, agentcontext.Turn{UserID: user, ConversationID: conversation.ID, Token: token, Goal: "no-target", Epoch: snap.Epoch, PreferencesHash: assistantmemory.Fingerprint(snap)}, "以后项目优先 Go")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = mem.Propose(ctx, user, "no-target", assistantmemory.Proposal{ConversationID: conversation.ID, SourceMessageID: turn.UserMessageID, SourceQuote: "以后项目优先 Go", Write: assistantmemory.Write{Key: assistantmemory.Languages, Value: json.RawMessage(`["Go"]`)}}); e != nil {
		t.Fatal(e)
	}
	if e = contexts.Finish(ctx, turn, "completed", map[string]int64{}); e != nil {
		t.Fatal(e)
	}
	_ = repo.EndRun(ctx, conversation.ID, token, false)
	server := httptest.NewServer(httpapi.NewRouter(httpapi.Dependencies{AgentService: s, AgentMemoryService: s, IdentityResolver: identity.DevResolver{DefaultUserID: user}, TargetService: targets}))
	defer server.Close()
	vite := fmt.Sprintf(`import { defineConfig } from 'vite'; import react from '@vitejs/plugin-react'; export default defineConfig({plugins:[react()],server:{host:'127.0.0.1',port:5273,strictPort:true,proxy:{'/api':{target:%q}}}})`, server.URL)
	html := `<!doctype html><html lang="zh"><meta charset="UTF-8"><title>Agent 记忆独立验收</title><body><h1>Agent 记忆独立验收 · 合成数据</h1><p>仅挂载原浮窗组件和真实记忆接口，不连接正式账号资料。不要在此输入真实资料。</p><div id="root"></div><script type="module">import React from 'react';import{createRoot}from'react-dom/client';import '/src/styles.css';import{AgentWidget}from'/src/components/AgentWidget.tsx';createRoot(document.getElementById('root')).render(React.createElement(AgentWidget,{currentPage:'验收',currentTarget:'合成测试目标',targetRevision:'test',nextStepPrompt:''}));</script></body></html>`
	for name, content := range map[string]string{"memory-vite.config.ts": vite, "memory-preview.html": html} {
		if e = os.WriteFile(filepath.Join(root, name), []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
	}
	t.Log("Browser fixture ready at http://127.0.0.1:5273/.cache/memory-preview.html")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	timeout := time.NewTimer(20 * time.Minute)
	defer timeout.Stop()
	for {
		select {
		case <-ticker.C:
			if _, e = os.Stat(filepath.Join(root, "browser.done")); e == nil {
				return
			}
		case <-timeout.C:
			t.Fatal("browser fixture timed out")
		case <-t.Context().Done():
			return
		}
	}
}
