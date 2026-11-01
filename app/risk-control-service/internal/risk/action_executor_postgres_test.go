package risk

import (
	"context"
	"errors"
	"testing"
)

func TestPolicyActionExecutorActivatesRestrictionExactlyOnce(t *testing.T) {
	ctx := context.Background()
	pool := newPolicyPublisherTestPool(t, ctx)
	runtime, err := NewPolicySnapshotRuntime(compileRuntimeTestSnapshotWithAction(t, 1, "block"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewRealtimeDecisionService(pool, runtime)
	decision, replay, err := service.Decide(ctx, RealtimeDecisionCommand{
		EvaluationID: "decision-executor-1",
		SubjectType:  "organization",
		SubjectID:    "org-risky",
		Params:       map[string]any{"amount_minor": int64(150_000)},
	})
	if err != nil || replay || len(decision.ActionJobs) != 1 {
		t.Fatalf("decision=%+v replay=%t err=%v", decision, replay, err)
	}

	executor, err := NewPolicyActionExecutor(
		pool,
		[]PolicyActionEffect{NewActivateRestrictionEffect(pool)},
		3,
	)
	if err != nil {
		t.Fatal(err)
	}
	processed, job, err := executor.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !processed || job.ActionID != decision.ActionJobs[0].ActionID || job.Status != "completed" || job.AttemptCount != 1 {
		t.Fatalf("unexpected executed job: processed=%t job=%+v", processed, job)
	}
	processed, _, err = executor.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if processed {
		t.Fatal("completed action must not be claimed twice")
	}

	check, err := NewPostgresStore(pool).CheckRestriction(ctx, RestrictionCheckCommand{
		SubjectType: "organization", SubjectID: "org-risky", Capability: "withdrawal",
	})
	if err != nil {
		t.Fatal(err)
	}
	if check.Decision != "deny" || check.RestrictionID == "" {
		t.Fatalf("restriction was not enforced: %+v", check)
	}
}

func TestPolicyActionExecutorRetriesFailureWithoutCreatingAnotherJob(t *testing.T) {
	ctx := context.Background()
	pool := newPolicyPublisherTestPool(t, ctx)
	runtime, err := NewPolicySnapshotRuntime(compileRuntimeTestSnapshotWithAction(t, 1, "block"))
	if err != nil {
		t.Fatal(err)
	}
	decision, _, err := NewRealtimeDecisionService(pool, runtime).Decide(ctx, RealtimeDecisionCommand{
		EvaluationID: "decision-retry-1",
		SubjectType:  "organization",
		SubjectID:    "org-retry",
		Params:       map[string]any{"amount_minor": int64(150_000)},
	})
	if err != nil {
		t.Fatal(err)
	}
	effect := &flakyPolicyActionEffect{failuresRemaining: 1}
	executor, err := NewPolicyActionExecutor(pool, []PolicyActionEffect{effect}, 3)
	if err != nil {
		t.Fatal(err)
	}

	processed, failed, err := executor.RunOnce(ctx)
	if !processed || err == nil || failed.Status != "retryable" || failed.AttemptCount != 1 {
		t.Fatalf("first attempt: processed=%t job=%+v err=%v", processed, failed, err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE risk_policy_action_jobs SET next_attempt_at=NOW()
		WHERE action_id=$1`, decision.ActionJobs[0].ActionID); err != nil {
		t.Fatal(err)
	}
	processed, completed, err := executor.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !processed || completed.Status != "completed" || completed.AttemptCount != 2 || effect.calls != 2 {
		t.Fatalf("second attempt: processed=%t job=%+v calls=%d", processed, completed, effect.calls)
	}
	var actionRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM risk_policy_action_jobs WHERE evaluation_id='decision-retry-1'`).Scan(&actionRows); err != nil {
		t.Fatal(err)
	}
	if actionRows != 1 {
		t.Fatalf("retry created %d action jobs", actionRows)
	}
}

type flakyPolicyActionEffect struct {
	failuresRemaining int
	calls             int
}

func (*flakyPolicyActionEffect) ActionCode() string { return "activate_restriction" }

func (e *flakyPolicyActionEffect) Execute(context.Context, PolicyActionExecution) error {
	e.calls++
	if e.failuresRemaining > 0 {
		e.failuresRemaining--
		return errors.New("temporary dependency failure")
	}
	return nil
}
