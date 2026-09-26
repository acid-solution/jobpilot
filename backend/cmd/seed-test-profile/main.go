// seed-test-profile creates an isolated, explicitly synthetic local account.
// Run from the backend directory. It never modifies an existing account.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
)

type req struct {
	op, kind, evidence string
	n                  int
	abilities          []string
	level              int
}

type jdSample struct {
	title, specialty, responsibility string
	requirements                     []req
}

func single(ability string, level int) req {
	depth := "熟悉"
	if level >= 3 {
		depth = "能够独立使用"
	}
	if level >= 4 {
		depth = "能够设计和优化"
	}
	return req{"single", "required", depth + ability + "完成相关开发工作", 1, []string{ability}, level}
}

func samples() []jdSample {
	return []jdSample{
		{"Go 业务后端开发实习生", "backend-business", "负责用户账户与业务接口开发，处理权限和数据一致性问题。", []req{single("Go", 3), single("Gin", 2), single("MySQL", 3), single("Git", 2)}},
		{"内容平台后端开发实习生", "backend-business", "负责内容发布与检索服务的接口设计、数据模型和缓存策略。", []req{single("Go", 3), single("Redis", 3), single("MySQL", 2), single("计算机网络", 2)}},
		{"研发工具平台后端实习生", "backend-developer-platform", "建设供研发团队使用的发布平台、配置接口和任务状态管理能力。", []req{single("Go", 3), single("PostgreSQL", 3), single("Docker", 2), single("软件设计", 3)}},
		{"服务框架研发实习生", "backend-framework-middleware", "开发供多个业务复用的服务框架组件，优化超时、重试和并发处理。", []req{single("Go", 4), single("计算机网络", 3), single("操作系统", 3), single("数据结构与算法", 3)}},
		{"智能问答后端实习生", "ai-rag-knowledge-base", "建设知识库问答服务，处理检索结果、引用依据和回答质量评估。", []req{single("RAG", 3), single("Python", 2), single("PostgreSQL", 2), single("大模型基础", 2)}},
		{"Agent 应用开发实习生", "ai-agent-application", "开发可调用业务工具的 Agent 应用，设计工具参数校验与失败恢复。", []req{single("Function Calling", 3), single("MCP 开发", 2), single("Go", 2), single("大模型基础", 2)}},
		{"企业 AI 应用平台后端实习生", "ai-application-platform", "建设多租户 AI 应用平台的模型接入、调用记录与权限管理服务。", []req{single("Go", 3), single("PostgreSQL", 3), single("大模型基础", 2), single("Redis", 2)}},
		{"高并发业务后端实习生", "backend-business", "开发订单业务接口，解决缓存失效与数据库事务边界问题。", []req{single("Go", 3), single("MySQL", 3), single("Redis", 3), single("软件设计", 2)}},
		{"开放 API 平台后端实习生", "backend-developer-platform", "建设面向开发者的开放接口、密钥管理和调用审计服务。", []req{single("Go", 3), single("Gin", 3), single("PostgreSQL", 2), single("Docker", 2)}},
		{"智能工作流后端实习生", "ai-agent-application", "开发可编排业务工具的工作流，处理执行状态、重试和用户确认。", []req{{"any_of", "required", "掌握 Go 或 Python 任意一门语言开发服务", 1, []string{"Go", "Python"}, 3}, single("Function Calling", 3), single("MCP 开发", 2), single("数据结构与算法", 2)}},
	}
}

func dotenv(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key+"=") {
			return strings.Trim(strings.TrimPrefix(line, key+"="), "\"'\r")
		}
	}
	return ""
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	backendDSN := dotenv(".env.local", "DATABASE_URL")
	if backendDSN == "" {
		backendDSN = dotenv(".env", "DATABASE_URL")
	}
	if backendDSN == "" {
		backendDSN = "postgres://jobpilot:jobpilot@127.0.0.1:5433/jobpilot?sslmode=disable"
	}
	authDSN := dotenv(filepath.Join("..", "..", "shared-auth", ".env"), "DATABASE_URL")
	if authDSN == "" {
		panic("shared-auth/.env DATABASE_URL not found")
	}
	authDB, err := sql.Open("pgx", authDSN)
	must(err)
	defer authDB.Close()
	backendDB, err := sql.Open("pgx", backendDSN)
	must(err)
	defer backendDB.Close()
	must(authDB.PingContext(ctx))
	must(backendDB.PingContext(ctx))

	secret := make([]byte, 21)
	_, err = rand.Read(secret)
	must(err)
	password := base64.RawURLEncoding.EncodeToString(secret)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	must(err)
	suffix := hex.EncodeToString(secret[:5])
	email := "jobpilot.test." + suffix + "@example.test"
	userID, targetID := uuid.New(), uuid.New()
	authTx, err := authDB.BeginTx(ctx, nil)
	must(err)
	_, err = authTx.ExecContext(ctx, `INSERT INTO users(id,password_hash) VALUES($1,$2)`, userID, string(hash))
	if err == nil {
		_, err = authTx.ExecContext(ctx, `INSERT INTO user_identities(id,user_id,kind,normalized_value,verified_at) VALUES($1,$2,'email',$3,NOW())`, uuid.New(), userID, email)
	}
	if err != nil {
		_ = authTx.Rollback()
		panic(err)
	}
	must(authTx.Commit())
	committed := false
	defer func() {
		if !committed {
			_, _ = authDB.ExecContext(context.Background(), `DELETE FROM users WHERE id=$1`, userID)
		}
	}()

	tx, err := backendDB.BeginTx(ctx, nil)
	must(err)
	defer tx.Rollback()
	categoryIDs := map[string]uuid.UUID{}
	for _, code := range []string{"backend", "ai-application"} {
		var id uuid.UUID
		must(tx.QueryRowContext(ctx, `SELECT id FROM job_categories WHERE code=$1 AND is_active`, code).Scan(&id))
		categoryIDs[code] = id
	}
	directions := []map[string]string{{"category": "后端开发", "specialty": ""}, {"category": "AI 应用开发", "specialty": ""}}
	directionsJSON, err := json.Marshal(directions)
	must(err)
	_, err = tx.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type,graduation_year,directions,catalog_status) VALUES($1,$2,'后端开发＋AI 应用开发','internship',2028,$3,'valid')`, targetID, userID, directionsJSON)
	must(err)
	for i, code := range []string{"backend", "ai-application"} {
		_, err = tx.ExecContext(ctx, `INSERT INTO target_directions(target_id,category_id,sort_order) VALUES($1,$2,$3)`, targetID, categoryIDs[code], i+1)
		must(err)
	}

	abilityIDs := map[string]uuid.UUID{}
	abilityCodes := map[string]string{}
	for _, sample := range samples() {
		for _, requirement := range sample.requirements {
			for _, name := range requirement.abilities {
				if _, exists := abilityIDs[name]; exists {
					continue
				}
				var id uuid.UUID
				var code string
				must(tx.QueryRowContext(ctx, `SELECT id,code FROM abilities WHERE name=$1 AND is_active`, name).Scan(&id, &code))
				abilityIDs[name], abilityCodes[name] = id, code
			}
		}
	}
	for index, sample := range samples() {
		var catID, specID uuid.UUID
		var categoryName, specialtyName string
		must(tx.QueryRowContext(ctx, `SELECT c.id,s.id,c.name,s.name FROM job_specialties s JOIN job_categories c ON c.id=s.category_id WHERE s.code=$1 AND s.is_active`, sample.specialty).Scan(&catID, &specID, &categoryName, &specialtyName))
		reqTexts := []string{}
		for _, q := range sample.requirements {
			reqTexts = append(reqTexts, q.evidence)
		}
		raw := fmt.Sprintf("【测试数据 %02d】\n岗位名称：%s\n公司：测试\n求职类型：实习\n岗位职责：%s\n岗位要求：%s。\n说明：本条为 JobPilot 功能验证用的虚构招聘说明，不对应真实招聘岗位。", index+1, sample.title, sample.responsibility, strings.Join(reqTexts, "；"))
		rawHash := sha256.Sum256([]byte(raw))
		jdID := uuid.New()
		responsibilities, _ := json.Marshal([]string{sample.responsibility})
		mentions := []map[string]any{}
		for _, q := range sample.requirements {
			for _, name := range q.abilities {
				mentions = append(mentions, map[string]any{"name": name, "catalog_code": abilityCodes[name], "evidence": q.evidence, "required_level": q.level})
			}
		}
		mentionsJSON, _ := json.Marshal(mentions)
		_, err = tx.ExecContext(ctx, `INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,title,company,status,primary_category,relevance_reason,employment_type,responsibilities,ability_mentions,analysis_provider,analysis_model,analysis_prompt_version,analysis_completed_at,document_type,validation_status,classification_review_decision,classification_review_reason,classification_reviewed_at) VALUES($1,$2,$3,$4,$5,$6,'测试','included',$7,'测试样本：主导分类命中当前目标方向。','internship',$8,$9,'fixture','synthetic','fixture-v1',NOW(),'job_description','valid','accept','测试样本预设分类，未调用模型。',NOW())`, jdID, userID, targetID, raw, hex.EncodeToString(rawHash[:]), sample.title, categoryName+" / "+specialtyName, responsibilities, mentionsJSON)
		must(err)
		_, err = tx.ExecContext(ctx, `INSERT INTO analysis_jobs(id,user_id,target_id,job_description_id,job_type,status,attempts) VALUES($1,$2,$3,$4,'jd_analysis','succeeded',1)`, uuid.New(), userID, targetID, jdID)
		must(err)
		_, err = tx.ExecContext(ctx, `INSERT INTO job_description_classifications(job_description_id,category_id,specialty_id,relation,evidence,reason) VALUES($1,$2,$3,'primary',$4,'该职责以对应岗位方向的工作为主要交付。')`, jdID, catID, specID, sample.responsibility)
		must(err)
		for ri, q := range sample.requirements {
			reqID := uuid.New()
			_, err = tx.ExecContext(ctx, `INSERT INTO job_description_ability_requirements(id,job_description_id,operator,required_count,evidence,sort_order,requirement_kind) VALUES($1,$2,$3,$4,$5,$6,$7)`, reqID, jdID, q.op, q.n, q.evidence, ri+1, q.kind)
			must(err)
			for oi, name := range q.abilities {
				optionID, abilityID := uuid.New(), abilityIDs[name]
				_, err = tx.ExecContext(ctx, `INSERT INTO job_description_ability_requirement_options(id,requirement_id,ability_id,raw_label,evidence,required_level,sort_order,resolution_status) VALUES($1,$2,$3,$4,$5,$6,$7,'resolved')`, optionID, reqID, abilityID, name, q.evidence, q.level, oi+1)
				must(err)
				_, err = tx.ExecContext(ctx, `INSERT INTO jd_ability_option_levels(option_id,job_description_id,ability_id,level,source,requirement_kind,evidence_quote,reason,confidence,prompt_version,level_standard_version) VALUES($1,$2,$3,$4,'explicit',$5,$6,'测试样本明确写出技能深度，仅供界面和流程验证。',0.9,'fixture-v1',1)`, optionID, jdID, abilityID, q.level, q.kind, q.evidence)
				must(err)
				_, err = tx.ExecContext(ctx, `INSERT INTO jd_ability_level_assessments(job_description_id,ability_id,level,source,requirement_kind,evidence_quote,reason,confidence,level_standard_version,prompt_version) VALUES($1,$2,$3,'explicit',$4,$5,'测试样本明确写出技能深度，仅供界面和流程验证。',0.9,1,'fixture-v1') ON CONFLICT(job_description_id,ability_id,requirement_kind) DO UPDATE SET level=GREATEST(jd_ability_level_assessments.level,EXCLUDED.level)`, jdID, abilityID, q.level, q.kind, q.evidence)
				must(err)
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO jd_ability_level_jobs(user_id,target_id,job_description_id,status,attempts,prompt_version,completed_at) VALUES($1,$2,$3,'succeeded',1,'fixture-v1',NOW())`, userID, targetID, jdID)
		must(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO user_profile_settings(user_id,weekly_hours,expected_weeks,existing_experience) VALUES($1,12,10,'【测试数据】做过 Go/Gin 接口、关系型数据库和基础缓存练习；尝试过 RAG 与工具调用。以下能力等级均为虚构测试值，仅用于验证产品流程。')`, userID)
	must(err)
	materialID := uuid.New()
	materialText := "【测试数据】在课程实践中使用 Go 和 Gin 开发过多用户任务管理接口，使用 MySQL 保存任务，使用 Redis 缓存列表，使用 Git 管理代码；另做过基础 RAG 问答演示。此经历为虚构测试数据。"
	_, err = tx.ExecContext(ctx, `INSERT INTO user_profile_materials(id,user_id,type,title,source_text,status,confirmed_at) VALUES($1,$2,'experience','测试经历自述',$3,'ready',NOW())`, materialID, userID, materialText)
	must(err)
	userLevels := map[string]int{"Go": 3, "Gin": 3, "MySQL": 2, "Redis": 2, "Git": 2, "RAG": 1, "PostgreSQL": 1, "Docker": 1, "软件设计": 2, "计算机网络": 2, "操作系统": 2, "数据结构与算法": 2, "Python": 1, "大模型基础": 1, "Function Calling": 1, "MCP 开发": 0}
	for name, abilityID := range abilityIDs {
		level, exists := userLevels[name]
		if !exists {
			panic("missing synthetic user level: " + name)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO user_capability_profiles(user_id,ability_id,current_level,evidence_level,verified_level,manual_level,manual_updated_at,level_source) VALUES($1,$2,$3,0,0,$3,NOW(),'manual')`, userID, abilityID, level)
		must(err)
		_, err = tx.ExecContext(ctx, `INSERT INTO user_capability_level_events(user_id,ability_id,new_level,source) VALUES($1,$2,$3,'manual')`, userID, abilityID, level)
		must(err)
	}
	for name, quote := range map[string]string{"Go": "使用 Go 和 Gin 开发过多用户任务管理接口", "Gin": "使用 Go 和 Gin 开发过多用户任务管理接口", "MySQL": "使用 MySQL 保存任务", "Redis": "使用 Redis 缓存列表", "Git": "使用 Git 管理代码", "RAG": "做过基础 RAG 问答演示"} {
		_, err = tx.ExecContext(ctx, `INSERT INTO user_profile_evidence(material_id,ability_id,level,evidence_quote,reason,confidence) VALUES($1,$2,$3,$4,'虚构测试经历中的直接文字证据。',0.8)`, materialID, abilityIDs[name], max(1, userLevels[name]), quote)
		must(err)
	}
	must(tx.Commit())
	committed = true
	credentialPath := filepath.Join("..", "..", "jobpilot-test-account-"+suffix+".local.txt")
	content := fmt.Sprintf("JobPilot 本机测试账号（虚构数据）\n邮箱：%s\n密码：%s\n账号 ID：%s\n目标 ID：%s\n说明：仅用于本机功能验证，请勿部署到线上或当作真实求职资料。\n", email, password, userID, targetID)
	if err := os.WriteFile(credentialPath, []byte(content), 0600); err != nil {
		panic(err)
	}
	var jdCount, abilityCount int
	must(backendDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_descriptions WHERE user_id=$1 AND status='included' AND validation_status='valid'`, userID).Scan(&jdCount))
	must(backendDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_capability_profiles WHERE user_id=$1 AND manual_level IS NOT NULL`, userID).Scan(&abilityCount))
	if jdCount != 10 || abilityCount != len(abilityIDs) {
		panic(errors.New("fixture verification failed"))
	}
	fmt.Printf("created isolated account: %s\nuser_id=%s target_id=%s included_jds=%d assessed_abilities=%d\ncredentials_file=%s\n", email, userID, targetID, jdCount, abilityCount, credentialPath)
}
