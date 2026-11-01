package risk

import "fmt"

type FactorValue struct {
	ValueType string
	Number    float64
	String    string
	Bool      bool
	Strings   []string
}

type FactorValues map[string]FactorValue

func NumberFactorValue(value float64) FactorValue {
	return FactorValue{ValueType: "number", Number: value}
}

func StringFactorValue(value string) FactorValue {
	return FactorValue{ValueType: "string", String: value}
}

func BoolFactorValue(value bool) FactorValue {
	return FactorValue{ValueType: "bool", Bool: value}
}

func StringListFactorValue(values []string) FactorValue {
	return FactorValue{ValueType: "string_list", Strings: append([]string(nil), values...)}
}

type FactorCondition struct {
	FactorKey    string
	Operator     string
	NumberValue  *float64
	StringValue  *string
	StringValues []string
	BoolValue    *bool
}

type ConditionNode struct {
	Condition *FactorCondition
	All       []ConditionNode
	Any       []ConditionNode
	Not       *ConditionNode
}

type DecisionTemplate struct {
	Decision    string
	ReasonCodes []string
}

type DecisionPolicy struct {
	PolicyKey string
	Version   int
	When      ConditionNode
	Matched   DecisionTemplate
	Unmatched DecisionTemplate
}

type PolicyDecision struct {
	Decision    string
	ReasonCodes []string
}

func EvaluateDecisionPolicy(policy DecisionPolicy, values FactorValues) (PolicyDecision, error) {
	if err := ValidateDecisionPolicy(policy); err != nil {
		return PolicyDecision{}, err
	}
	matched, err := evaluateConditionNode(policy.When, values)
	if err != nil {
		return PolicyDecision{}, err
	}
	template := policy.Unmatched
	if matched {
		template = policy.Matched
	}
	return PolicyDecision{Decision: template.Decision, ReasonCodes: append([]string(nil), template.ReasonCodes...)}, nil
}

func ValidateDecisionPolicy(policy DecisionPolicy) error {
	if policy.PolicyKey == "" || policy.Version <= 0 {
		return fmt.Errorf("policy_key and positive version are required")
	}
	if policy.Matched.Decision == "" || policy.Unmatched.Decision == "" {
		return fmt.Errorf("matched and unmatched decisions are required")
	}
	return validateConditionNode(policy.When)
}

func validateConditionNode(node ConditionNode) error {
	kinds := 0
	if node.Condition != nil {
		kinds++
	}
	if len(node.All) > 0 {
		kinds++
	}
	if len(node.Any) > 0 {
		kinds++
	}
	if node.Not != nil {
		kinds++
	}
	if kinds != 1 {
		return fmt.Errorf("condition node must define exactly one of condition, all, any or not")
	}
	if node.Condition != nil {
		if node.Condition.FactorKey == "" || node.Condition.Operator == "" {
			return fmt.Errorf("factor_key and operator are required")
		}
		return nil
	}
	children := node.All
	if len(node.Any) > 0 {
		children = node.Any
	}
	if node.Not != nil {
		children = []ConditionNode{*node.Not}
	}
	for _, child := range children {
		if err := validateConditionNode(child); err != nil {
			return err
		}
	}
	return nil
}

func evaluateConditionNode(node ConditionNode, values FactorValues) (bool, error) {
	if len(node.All) > 0 {
		for _, child := range node.All {
			matched, err := evaluateConditionNode(child, values)
			if err != nil {
				return false, err
			}
			if !matched {
				return false, nil
			}
		}
		return true, nil
	}
	if len(node.Any) > 0 {
		for _, child := range node.Any {
			matched, err := evaluateConditionNode(child, values)
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
		return false, nil
	}
	if node.Not != nil {
		matched, err := evaluateConditionNode(*node.Not, values)
		return !matched, err
	}
	if node.Condition == nil {
		return false, fmt.Errorf("condition node is empty")
	}
	condition := node.Condition
	actual, ok := values[condition.FactorKey]
	if condition.Operator == "exists" {
		return ok, nil
	}
	if condition.Operator == "not_exists" {
		return !ok, nil
	}
	if !ok {
		return false, fmt.Errorf("factor %q is missing", condition.FactorKey)
	}
	if actual.ValueType == "string" {
		return evaluateStringCondition(actual.String, *condition)
	}
	if actual.ValueType == "bool" {
		return evaluateBoolCondition(actual.Bool, *condition)
	}
	if actual.ValueType == "string_list" {
		return evaluateStringListCondition(actual.Strings, *condition)
	}
	if actual.ValueType != "number" || condition.NumberValue == nil {
		return false, fmt.Errorf("factor %q requires a numeric condition", condition.FactorKey)
	}
	switch condition.Operator {
	case "eq":
		return actual.Number == *condition.NumberValue, nil
	case "ne":
		return actual.Number != *condition.NumberValue, nil
	case "gt":
		return actual.Number > *condition.NumberValue, nil
	case "gte":
		return actual.Number >= *condition.NumberValue, nil
	case "lt":
		return actual.Number < *condition.NumberValue, nil
	case "lte":
		return actual.Number <= *condition.NumberValue, nil
	default:
		return false, fmt.Errorf("operator %q is not supported", condition.Operator)
	}
}

func evaluateStringCondition(actual string, condition FactorCondition) (bool, error) {
	switch condition.Operator {
	case "eq", "ne":
		if condition.StringValue == nil {
			return false, fmt.Errorf("factor %q requires a string value", condition.FactorKey)
		}
		matched := actual == *condition.StringValue
		if condition.Operator == "ne" {
			matched = !matched
		}
		return matched, nil
	case "in", "not_in":
		matched := false
		for _, expected := range condition.StringValues {
			if actual == expected {
				matched = true
				break
			}
		}
		if condition.Operator == "not_in" {
			matched = !matched
		}
		return matched, nil
	default:
		return false, fmt.Errorf("operator %q is not supported for string factor %q", condition.Operator, condition.FactorKey)
	}
}

func evaluateBoolCondition(actual bool, condition FactorCondition) (bool, error) {
	if condition.BoolValue == nil || (condition.Operator != "eq" && condition.Operator != "ne") {
		return false, fmt.Errorf("factor %q requires a boolean eq or ne condition", condition.FactorKey)
	}
	matched := actual == *condition.BoolValue
	if condition.Operator == "ne" {
		matched = !matched
	}
	return matched, nil
}

func evaluateStringListCondition(actual []string, condition FactorCondition) (bool, error) {
	if condition.StringValue == nil || condition.Operator != "contains" {
		return false, fmt.Errorf("factor %q requires a string contains condition", condition.FactorKey)
	}
	for _, value := range actual {
		if value == *condition.StringValue {
			return true, nil
		}
	}
	return false, nil
}
