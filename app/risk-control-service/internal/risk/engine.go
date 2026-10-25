package risk

import "slices"

func Evaluate(facts CustomerFacts, policy Policy) (string, []string, []string) {
	if slices.Contains(policy.DeniedResidenceCountries, facts.ResidenceCountry) {
		return "deny", []string{"residence_country_denied"}, nil
	}
	actions := make([]string, 0, 2)
	reasons := make([]string, 0, 2)
	if facts.VerificationStatus != policy.RequiredVerification {
		reasons = append(reasons, "verification_incomplete")
		actions = append(actions, "complete_verification")
	}
	if policy.RequirePOA && !facts.POAVerified {
		reasons = append(reasons, "poa_required")
		actions = append(actions, "provide_poa")
	}
	if policy.RequireQuestionnaire && !facts.QuestionnaireCompleted {
		reasons = append(reasons, "questionnaire_required")
		actions = append(actions, "complete_questionnaire")
	}
	if len(actions) > 0 {
		return "review", reasons, actions
	}
	return "allow", []string{"policy_satisfied"}, nil
}
