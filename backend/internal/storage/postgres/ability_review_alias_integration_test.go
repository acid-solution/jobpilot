package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilityreview"
	"github.com/google/uuid"
)

func TestAbilityReviewAliasConflictsIsolated(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	repo := NewAbilityReviewRepository(db)
	catalog, err := repo.loadAbilityCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var goID uuid.UUID
	var originalGoAliases string
	if err := db.QueryRowContext(ctx, `SELECT id,aliases::text FROM abilities WHERE name='Go'`).Scan(&goID, &originalGoAliases); err != nil {
		t.Fatal(err)
	}

	// Keep a real pending JD option so the test also catches incorrect business
	// associations, rather than only checking the review's decision label.
	newInput := func(t *testing.T) (abilityreview.Input, uuid.UUID) {
		t.Helper()
		userID, targetID, jdID := uuid.New(), uuid.New(), uuid.New()
		requestID, token, requirementID, optionID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		name := "AliasReview" + requestID.String()[:8]
		exec := func(query string, args ...any) {
			t.Helper()
			if _, err := db.ExecContext(ctx, query, args...); err != nil {
				t.Fatal(err)
			}
		}
		exec(`INSERT INTO job_targets(id,user_id,title,employment_type) VALUES($1,$2,'后端开发','internship')`, targetID, userID)
		exec(`INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status,validation_status)
			VALUES($1,$2,$3,$4,$5,'included','valid')`, jdID, userID, targetID, "掌握 "+name, jdID.String())
		exec(`INSERT INTO ability_review_requests(id,candidate_key,normalized_name,proposed_name,proposed_category_code,
			initiated_by_user_id,status,attempts,lease_token,heartbeat_at,lease_expires_at)
			VALUES($1,$2,$3,$4,$5,$6,'running',1,$7,NOW(),NOW()+INTERVAL '5 minutes')`,
			requestID, catalog[0].CategoryCode+":"+NormalizeAbilityName(name), NormalizeAbilityName(name), name, catalog[0].CategoryCode, userID, token)
		exec(`INSERT INTO job_description_ability_requirements(id,job_description_id,operator,required_count,evidence,sort_order)
			VALUES($1,$2,'single',1,$3,1)`, requirementID, jdID, "掌握 "+name)
		exec(`INSERT INTO job_description_ability_requirement_options(id,requirement_id,raw_label,evidence,sort_order,resolution_status,review_request_id)
			VALUES($1,$2,$3,$4,1,'pending_review',$5)`, optionID, requirementID, name, "掌握 "+name, requestID)
		return abilityreview.Input{ID: requestID, LeaseToken: token, Catalog: catalog, Attempts: 1, MaxAttempts: 3}, optionID
	}
	newResult := func(name string, aliases []string) abilityreview.Result {
		levels := make([]abilityreview.Level, 6)
		for i := range levels {
			levels[i] = abilityreview.Level{Level: i, Description: fmt.Sprintf("测试等级 L%d", i)}
		}
		return abilityreview.Result{Decision: "approve_new", Reason: "独立且可评估的能力", Provider: "test", Model: "test",
			PromptVersion: abilityreview.PromptVersion, NewAbility: abilityreview.NewAbility{Name: name, CategoryCode: catalog[0].CategoryCode,
				Definition: "测试独立能力", Aliases: aliases, Levels: levels}}
	}
	checkResolved := func(t *testing.T, input abilityreview.Input, optionID uuid.UUID, decision string) uuid.UUID {
		t.Helper()
		var actualDecision, status, resolution string
		var resolvedID, optionAbilityID uuid.UUID
		if err := db.QueryRowContext(ctx, `SELECT decision,status,resolved_ability_id FROM ability_review_requests WHERE id=$1`, input.ID).
			Scan(&actualDecision, &status, &resolvedID); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT ability_id,resolution_status FROM job_description_ability_requirement_options WHERE id=$1`, optionID).
			Scan(&optionAbilityID, &resolution); err != nil {
			t.Fatal(err)
		}
		if actualDecision != decision || status != "succeeded" || optionAbilityID != resolvedID || resolution != "resolved" {
			t.Fatalf("decision=%s status=%s review ability=%s option ability=%s resolution=%s", actualDecision, status, resolvedID, optionAbilityID, resolution)
		}
		var publicCount int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_description_abilities mention
			JOIN job_description_ability_requirements requirement ON requirement.job_description_id=mention.job_description_id
			JOIN job_description_ability_requirement_options option ON option.requirement_id=requirement.id
			WHERE option.id=$1 AND mention.ability_id=$2`, optionID, resolvedID).Scan(&publicCount); err != nil || publicCount != 1 {
			t.Fatalf("public mentions=%d err=%v", publicCount, err)
		}
		return resolvedID
	}

	for _, onlyConflicting := range []bool{false, true} {
		t.Run(fmt.Sprintf("alias_conflict_only_%t", onlyConflicting), func(t *testing.T) {
			input, optionID := newInput(t)
			name := "NewFramework" + uuid.NewString()[:8]
			aliases := []string{"Go", "Golang", "Java"}
			wantAliases := []string{}
			if !onlyConflicting {
				uniqueAlias := "FrameworkAlias" + uuid.NewString()[:8]
				aliases = append(aliases, "  "+uniqueAlias+"  ", uniqueAlias, name)
				wantAliases = append(wantAliases, uniqueAlias)
			}
			result := newResult(name, aliases)
			if err := repo.Complete(ctx, input, result, uuid.Nil); err != nil {
				t.Fatal(err)
			}
			id := checkResolved(t, input, optionID, "approve_new")
			if id == goID {
				t.Fatal("an alias collision merged an independent ability into Go")
			}
			var actualName, definition string
			var encodedAliases []byte
			if err := db.QueryRowContext(ctx, `SELECT name,definition,aliases FROM abilities WHERE id=$1`, id).Scan(&actualName, &definition, &encodedAliases); err != nil {
				t.Fatal(err)
			}
			var actualAliases []string
			if err := json.Unmarshal(encodedAliases, &actualAliases); err != nil {
				t.Fatal(err)
			}
			if actualName != name || definition != result.NewAbility.Definition || !reflect.DeepEqual(actualAliases, wantAliases) {
				t.Fatalf("name=%s definition=%s aliases=%v want=%v", actualName, definition, actualAliases, wantAliases)
			}
			var levelCount int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ability_levels WHERE ability_id=$1`, id).Scan(&levelCount); err != nil || levelCount != 6 {
				t.Fatalf("levels=%d err=%v", levelCount, err)
			}
		})
	}
	for _, name := range []string{"Go", "Golang"} {
		t.Run("canonical_name_"+name, func(t *testing.T) {
			input, optionID := newInput(t)
			if err := repo.Complete(ctx, input, newResult(name, []string{"MustNotBeAdded"}), uuid.Nil); err != nil {
				t.Fatal(err)
			}
			if id := checkResolved(t, input, optionID, "reuse_existing"); id != goID {
				t.Fatalf("name match resolved to %s instead of Go %s", id, goID)
			}
		})
	}
	t.Run("concurrent_same_canonical_name", func(t *testing.T) {
		first, firstOption := newInput(t)
		second, secondOption := newInput(t)
		name := "ConcurrentFramework" + uuid.NewString()[:8]
		result := newResult(name, []string{"Go"})
		errors := make(chan error, 2)
		go func() { errors <- repo.Complete(ctx, first, result, uuid.Nil) }()
		go func() { errors <- repo.Complete(ctx, second, result, uuid.Nil) }()
		for range 2 {
			if err := <-errors; err != nil {
				t.Fatal(err)
			}
		}
		var count, approved, reused int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM abilities WHERE normalized_name=$1`, NormalizeAbilityName(name)).Scan(&count); err != nil || count != 1 {
			t.Fatalf("created=%d err=%v", count, err)
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE decision='approve_new'),COUNT(*) FILTER (WHERE decision='reuse_existing')
			FROM ability_review_requests WHERE id IN ($1,$2)`, first.ID, second.ID).Scan(&approved, &reused); err != nil || approved != 1 || reused != 1 {
			t.Fatalf("approved=%d reused=%d err=%v", approved, reused, err)
		}
		var firstAbility, secondAbility uuid.UUID
		if err := db.QueryRowContext(ctx, `SELECT ability_id FROM job_description_ability_requirement_options WHERE id=$1`, firstOption).Scan(&firstAbility); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT ability_id FROM job_description_ability_requirement_options WHERE id=$1`, secondOption).Scan(&secondAbility); err != nil {
			t.Fatal(err)
		}
		if firstAbility != secondAbility || firstAbility == goID {
			t.Fatalf("concurrent resolutions disagree or incorrectly reuse Go: %s %s", firstAbility, secondAbility)
		}
	})
	var goAliases string
	if err := db.QueryRowContext(ctx, `SELECT aliases::text FROM abilities WHERE id=$1`, goID).Scan(&goAliases); err != nil || goAliases != originalGoAliases {
		t.Fatalf("existing Go aliases changed: before=%s after=%s err=%v", originalGoAliases, goAliases, err)
	}
}
