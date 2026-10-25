package risk

import "context"

type Store interface {
	UpsertFacts(context.Context, CustomerFacts) (CustomerFacts, error)
	Evaluate(context.Context, EvaluationCommand) (Evaluation, bool, error)
	GetEvaluation(context.Context, string) (Evaluation, error)
	CompleteAction(context.Context, string, CompleteActionCommand) (Action, error)
}
