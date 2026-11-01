package risk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// NearlineDecisionEvent is the stable event contract consumed from Kafka.
// The event describes business facts only; policy selection remains server-owned.
type NearlineDecisionEvent struct {
	EventID      string         `json:"event_id"`
	OccurredAt   time.Time      `json:"occurred_at"`
	EvaluationID string         `json:"evaluation_id"`
	SubjectType  string         `json:"subject_type"`
	SubjectID    string         `json:"subject_id"`
	Params       map[string]any `json:"params"`
}

type NearlineDecisionResult struct {
	EventID  string                  `json:"event_id"`
	Replay   bool                    `json:"replay"`
	Decision DurableRealtimeDecision `json:"decision"`
}

type NearlineProcessor struct {
	decider RealtimeDecider
}

func NewNearlineProcessor(decider RealtimeDecider) *NearlineProcessor {
	return &NearlineProcessor{decider: decider}
}

func DecodeNearlineDecisionEvent(payload []byte) (NearlineDecisionEvent, error) {
	var event NearlineDecisionEvent
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&event); err != nil {
		return NearlineDecisionEvent{}, err
	}
	return event, nil
}

func (p *NearlineProcessor) Handle(
	ctx context.Context,
	event NearlineDecisionEvent,
) (NearlineDecisionResult, error) {
	if p == nil || p.decider == nil {
		return NearlineDecisionResult{}, fmt.Errorf("nearline decision processor is not configured")
	}
	if event.EventID == "" || event.OccurredAt.IsZero() ||
		event.EvaluationID == "" || event.SubjectType == "" || event.SubjectID == "" {
		return NearlineDecisionResult{}, fmt.Errorf(
			"event_id, occurred_at, evaluation_id, subject_type and subject_id are required",
		)
	}
	decision, replay, err := p.decider.Decide(ctx, RealtimeDecisionCommand{
		EvaluationID: event.EvaluationID,
		SubjectType:  event.SubjectType,
		SubjectID:    event.SubjectID,
		Params:       event.Params,
	})
	if err != nil {
		return NearlineDecisionResult{}, err
	}
	return NearlineDecisionResult{
		EventID: event.EventID, Replay: replay, Decision: decision,
	}, nil
}
