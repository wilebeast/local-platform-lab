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
