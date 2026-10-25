package risk

import "time"

type CustomerFacts struct {
	CustomerID             string `json:"customer_id"`
	VerificationStatus     string `json:"verification_status"`
	ResidenceCountry       string `json:"residence_country"`
	POAVerified            bool   `json:"poa_verified"`
	QuestionnaireCompleted bool   `json:"questionnaire_completed"`
	FactVersion            int64  `json:"fact_version"`
}

type Policy struct {
	ProductCode              string   `json:"product_code"`
	Version                  int      `json:"policy_version"`
	RequiredVerification     string   `json:"required_verification_status"`
	RequirePOA               bool     `json:"require_poa"`
	RequireQuestionnaire     bool     `json:"require_questionnaire"`
	DeniedResidenceCountries []string `json:"denied_residence_countries"`
}

type EvaluationCommand struct {
	EvaluationID string `json:"evaluation_id"`
	CustomerID   string `json:"customer_id"`
	ProductCode  string `json:"product_code"`
}

type Evaluation struct {
	EvaluationID  string        `json:"evaluation_id"`
	CustomerID    string        `json:"customer_id"`
	ProductCode   string        `json:"product_code"`
	PolicyVersion int           `json:"policy_version"`
	FactVersion   int64         `json:"fact_version"`
	Decision      string        `json:"decision"`
	ReasonCodes   []string      `json:"reason_codes"`
	FactSnapshot  CustomerFacts `json:"fact_snapshot"`
	Actions       []Action      `json:"actions"`
	EvaluatedAt   time.Time     `json:"evaluated_at"`
}

type Action struct {
	ActionID     string     `json:"action_id"`
	EvaluationID string     `json:"evaluation_id"`
	CustomerID   string     `json:"customer_id"`
	ActionType   string     `json:"action_type"`
	Status       string     `json:"status"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

type CompleteActionCommand struct {
	POAVerified            *bool `json:"poa_verified,omitempty"`
	QuestionnaireCompleted *bool `json:"questionnaire_completed,omitempty"`
}

type ConflictError struct{ Message string }

func (e ConflictError) Error() string { return e.Message }

type NotFoundError struct{ Message string }

func (e NotFoundError) Error() string { return e.Message }
