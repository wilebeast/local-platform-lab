package risk

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) LoadPublishedPolicyDefinition(
	ctx context.Context,
	policyKey string,
	version int,
) (StoredPolicyDefinition, error) {
	return loadPolicyDefinition(ctx, s.pool, policyKey, version, "published")
}

func loadPolicyDefinition(
	ctx context.Context,
	q rowQuerier,
	policyKey string,
	version int,
	status string,
) (StoredPolicyDefinition, error) {
	definition := StoredPolicyDefinition{}
	var defaultReasonsJSON []byte
	err := q.QueryRow(ctx, `
		SELECT policy_key, policy_version, default_decision, default_reason_codes
		FROM risk_decision_policy_versions
		WHERE policy_key=$1 AND policy_version=$2 AND status=$3`, policyKey, version, status).
		Scan(
			&definition.Policy.PolicyKey,
			&definition.Policy.Version,
			&definition.Policy.DefaultDecision.Decision,
			&defaultReasonsJSON,
		)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredPolicyDefinition{}, NotFoundError{"published policy version not found"}
	}
	if err != nil {
		return StoredPolicyDefinition{}, err
	}
	if err := json.Unmarshal(defaultReasonsJSON, &definition.Policy.DefaultDecision.ReasonCodes); err != nil {
		return StoredPolicyDefinition{}, err
	}

	factorRows, err := q.Query(ctx, `
		SELECT factor_key, factor_type, value_type, param_key, expression, resolver_key, timeout_ms
		FROM risk_decision_factor_definitions
		WHERE policy_key=$1 AND policy_version=$2
		ORDER BY factor_key`, policyKey, version)
	if err != nil {
		return StoredPolicyDefinition{}, err
	}
	for factorRows.Next() {
		var factor FactorDefinition
		var timeoutMilliseconds int64
		if err := factorRows.Scan(
			&factor.Key,
			&factor.Type,
			&factor.ValueType,
			&factor.ParamKey,
			&factor.Expression,
			&factor.ResolverKey,
			&timeoutMilliseconds,
		); err != nil {
			factorRows.Close()
			return StoredPolicyDefinition{}, err
		}
		factor.Timeout = time.Duration(timeoutMilliseconds) * time.Millisecond
		definition.Factors = append(definition.Factors, factor)
	}
	if err := factorRows.Err(); err != nil {
		factorRows.Close()
		return StoredPolicyDefinition{}, err
	}
	factorRows.Close()

	strategyRows, err := q.Query(ctx, `
		SELECT s.strategy_key, s.priority, s.decision, s.reason_codes,
		       r.rule_key, r.rule_version, r.expression
		FROM risk_decision_strategies s
		JOIN risk_decision_rules r
		  ON r.policy_key=s.policy_key
		 AND r.policy_version=s.policy_version
		 AND r.rule_key=s.rule_key
		WHERE s.policy_key=$1 AND s.policy_version=$2
		ORDER BY s.priority DESC, s.strategy_key`, policyKey, version)
	if err != nil {
		return StoredPolicyDefinition{}, err
	}
	strategyIndexes := make(map[string]int)
	for strategyRows.Next() {
		var strategy ExecutableStrategyDefinition
		var reasonsJSON []byte
		if err := strategyRows.Scan(
			&strategy.StrategyKey,
			&strategy.Priority,
			&strategy.Effect.Decision.Decision,
			&reasonsJSON,
			&strategy.Rule.RuleKey,
			&strategy.Rule.Version,
			&strategy.Rule.Expression,
		); err != nil {
			strategyRows.Close()
			return StoredPolicyDefinition{}, err
		}
		if err := json.Unmarshal(reasonsJSON, &strategy.Effect.Decision.ReasonCodes); err != nil {
			strategyRows.Close()
			return StoredPolicyDefinition{}, err
		}
		strategyIndexes[strategy.StrategyKey] = len(definition.Policy.Strategies)
		definition.Policy.Strategies = append(definition.Policy.Strategies, strategy)
	}
	if err := strategyRows.Err(); err != nil {
		strategyRows.Close()
		return StoredPolicyDefinition{}, err
	}
	strategyRows.Close()

	actionRows, err := q.Query(ctx, `
		SELECT strategy_key, action_code, params
		FROM risk_decision_strategy_actions
		WHERE policy_key=$1 AND policy_version=$2
		ORDER BY strategy_key, sequence_no`, policyKey, version)
	if err != nil {
		return StoredPolicyDefinition{}, err
	}
	defer actionRows.Close()
	for actionRows.Next() {
		var strategyKey string
		var action PolicyAction
		var paramsJSON []byte
		if err := actionRows.Scan(&strategyKey, &action.ActionCode, &paramsJSON); err != nil {
			return StoredPolicyDefinition{}, err
		}
		if err := json.Unmarshal(paramsJSON, &action.Params); err != nil {
			return StoredPolicyDefinition{}, err
		}
		strategyIndex, exists := strategyIndexes[strategyKey]
		if !exists {
			return StoredPolicyDefinition{}, errors.New("strategy action references an unloaded strategy")
		}
		definition.Policy.Strategies[strategyIndex].Effect.Actions = append(
			definition.Policy.Strategies[strategyIndex].Effect.Actions,
			action,
		)
	}
	if err := actionRows.Err(); err != nil {
		return StoredPolicyDefinition{}, err
	}
	return definition, nil
}
