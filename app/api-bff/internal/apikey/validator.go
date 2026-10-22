package apikey

import "context"

type ValidationRequest struct {
	MerchantID         string
	KeyID              string
	RequiredPermission string
	ClientIP           string
}

type ValidationResult struct {
	Valid     bool
	Forbidden bool
}

type Validator interface {
	Validate(context.Context, ValidationRequest) (ValidationResult, error)
}
