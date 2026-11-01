package risk

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type nearlineDeciderStub struct {
	commands []RealtimeDecisionCommand
}

func (s *nearlineDeciderStub) Decide(
	_ context.Context,
	command RealtimeDecisionCommand,
) (DurableRealtimeDecision, bool, error) {
	s.commands = append(s.commands, command)
	return DurableRealtimeDecision{
		EvaluationID: command.EvaluationID,
		SubjectType:  command.SubjectType,
		SubjectID:    command.SubjectID,
		PolicyEvaluation: PolicyEvaluation{
			Decision: "block",
		},
	}, len(s.commands) > 1, nil
}

func TestNearlineProcessorUsesDurableDecisionIdempotency(t *testing.T) {
	decider := &nearlineDeciderStub{}
	processor := NewNearlineProcessor(decider)
	event := NearlineDecisionEvent{
		EventID:      "event-nearline-1",
		OccurredAt:   time.Unix(1_793_500_000, 0).UTC(),
		EvaluationID: "evaluation-nearline-1",
		SubjectType:  "organization",
		SubjectID:    "org-nearline-risky",
		Params: map[string]any{
			"inflow_minor_24h":  int64(100_000),
			"outflow_minor_24h": int64(95_000),
		},
	}

	first, err := processor.Handle(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	second, err := processor.Handle(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}

	if first.Replay {
		t.Fatal("first delivery must not be a replay")
	}
	if !second.Replay {
		t.Fatal("second delivery must expose the durable decision replay")
	}
	if len(decider.commands) != 2 || decider.commands[0].EvaluationID != event.EvaluationID {
		t.Fatalf("unexpected decision commands: %+v", decider.commands)
	}
}

func TestNearlineProcessorRejectsIncompleteEventBeforeDecision(t *testing.T) {
	decider := &nearlineDeciderStub{}
	processor := NewNearlineProcessor(decider)

	if _, err := processor.Handle(context.Background(), NearlineDecisionEvent{
		EventID: "event-nearline-invalid",
	}); err == nil {
		t.Fatal("expected incomplete event to be rejected")
	}
	if len(decider.commands) != 0 {
		t.Fatal("invalid event must not reach the decision service")
	}
}

func TestDecodeNearlineDecisionEventPreservesIntegerFactors(t *testing.T) {
	event, err := DecodeNearlineDecisionEvent([]byte(`{
		"event_id":"event-nearline-json",
		"occurred_at":"2026-11-01T02:38:28Z",
		"evaluation_id":"evaluation-nearline-json",
		"subject_type":"organization",
		"subject_id":"org-nearline-json",
		"params":{"inflow_minor_24h":100000,"outflow_minor_24h":95000}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	value, ok := event.Params["inflow_minor_24h"].(json.Number)
	if !ok || value.String() != "100000" {
		t.Fatalf("expected integer JSON number, got %#v", event.Params["inflow_minor_24h"])
	}
}
