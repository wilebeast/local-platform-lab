package risk

import (
	"reflect"
	"testing"
)

func TestEvaluate(t *testing.T) {
	policy := Policy{RequiredVerification: "approved", RequirePOA: true, DeniedResidenceCountries: []string{"KP", "IR"}}
	tests := []struct {
		name string
		facts CustomerFacts
		decision string
		reasons []string
		actions []string
	}{
		{"missing evidence becomes review", CustomerFacts{VerificationStatus:"approved",ResidenceCountry:"SG"}, "review", []string{"poa_required"}, []string{"provide_poa"}},
		{"complete facts become allow", CustomerFacts{VerificationStatus:"approved",ResidenceCountry:"SG",POAVerified:true}, "allow", []string{"policy_satisfied"}, nil},
		{"denied country overrides remedial actions", CustomerFacts{VerificationStatus:"pending",ResidenceCountry:"KP"}, "deny", []string{"residence_country_denied"}, nil},
	}
	for _,tt:=range tests { t.Run(tt.name,func(t *testing.T){decision,reasons,actions:=Evaluate(tt.facts,policy);if decision!=tt.decision||!reflect.DeepEqual(reasons,tt.reasons)||!reflect.DeepEqual(actions,tt.actions){t.Fatalf("got %s %v %v",decision,reasons,actions)}}) }
}
