package types

type HealthResponse struct {
	Status            string `json:"status"`
	Service           string `json:"service"`
	DiscoveryProvider string `json:"discoveryProvider"`
}

type ProfileResponse struct {
	UserID             string `json:"userId"`
	VerificationStatus string `json:"verificationStatus"`
	Source             string `json:"source"`
}

type AccountSummaryResponse struct {
	AccountID string `json:"accountId"`
	Status    string `json:"status"`
	Source    string `json:"source"`
}

type AccountLimitsResponse struct {
	DailyLimitCents int64  `json:"dailyLimitCents"`
	Currency        string `json:"currency"`
	Source          string `json:"source"`
}
