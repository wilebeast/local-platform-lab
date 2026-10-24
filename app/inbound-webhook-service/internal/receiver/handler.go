package receiver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wilebeast/local-platform-lab/app/inbound-webhook-service/internal/provider"
)

const (
	HeaderKeyID            = "X-Provider-Key-Id"
	HeaderEventID          = "X-Provider-Event-Id"
	HeaderTimestamp        = "X-Provider-Timestamp"
	HeaderSignatureVersion = "X-Provider-Signature-Version"
	HeaderSignature        = "X-Provider-Signature"
	maximumClockSkew       = 5 * time.Minute
	maximumBodySize        = 1 << 20
	pathPrefix             = "/api/v3/provider-webhooks/"
)

type Integration struct {
	Code        string
	Domain      string
	EventPrefix string
	Adapter     provider.Adapter
	Credentials map[string]provider.Credential
}

type InboxEvent struct {
	IntegrationCode string
	Domain          string
	ExternalEventID string
	EventType       string
	EventVersion    int
	OccurredAt      int64
	CredentialKeyID string
	RawHeaders      map[string]string
	RawBody         []byte
	BodySHA256      string
}

type Inbox interface {
	Accept(ctx context.Context, event InboxEvent) (created bool, err error)
}

type Handler struct {
	integrations map[string]Integration
	inbox        Inbox
	now          func() time.Time
}

func NewHandler(integrations map[string]Integration, inbox Inbox, now func() time.Time) http.Handler {
	return &Handler{integrations: integrations, inbox: inbox, now: now}
}

type envelope struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Version    int             `json:"version"`
	OccurredAt int64           `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	integrationCode := strings.TrimPrefix(request.URL.Path, pathPrefix)
	if integrationCode == request.URL.Path || integrationCode == "" || strings.Contains(integrationCode, "/") {
		http.NotFound(response, request)
		return
	}
	integration, exists := h.integrations[integrationCode]
	if !exists {
		http.NotFound(response, request)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(response, request.Body, maximumBodySize))
	if err != nil {
		http.Error(response, "invalid body", http.StatusRequestEntityTooLarge)
		return
	}
	keyID := strings.TrimSpace(request.Header.Get(HeaderKeyID))
	eventID := strings.TrimSpace(request.Header.Get(HeaderEventID))
	timestamp, err := strconv.ParseInt(request.Header.Get(HeaderTimestamp), 10, 64)
	credential, credentialExists := integration.Credentials[keyID]
	if err != nil || eventID == "" || keyID == "" || !credentialExists || request.Header.Get(HeaderSignatureVersion) != "v1" {
		http.Error(response, "invalid signature metadata", http.StatusUnauthorized)
		return
	}
	requestTime := time.Unix(timestamp, 0)
	if delta := h.now().Sub(requestTime); delta > maximumClockSkew || delta < -maximumClockSkew {
		http.Error(response, "expired signature", http.StatusUnauthorized)
		return
	}
	signed := provider.SignedRequest{Timestamp: timestamp, EventID: eventID, Body: body, Signature: strings.TrimSpace(request.Header.Get(HeaderSignature))}
	if err := integration.Adapter.Verify(signed, credential); err != nil {
		http.Error(response, "invalid signature", http.StatusUnauthorized)
		return
	}
	var parsed envelope
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.ID == "" || parsed.ID != eventID || parsed.Type == "" || parsed.Version < 1 || parsed.OccurredAt < 1 || !strings.HasPrefix(parsed.Type, integration.EventPrefix) {
		http.Error(response, "invalid event", http.StatusBadRequest)
		return
	}
	bodyDigest := sha256.Sum256(body)
	created, err := h.inbox.Accept(request.Context(), InboxEvent{
		IntegrationCode: integration.Code,
		Domain:          integration.Domain,
		ExternalEventID: parsed.ID,
		EventType:       parsed.Type,
		EventVersion:    parsed.Version,
		OccurredAt:      parsed.OccurredAt,
		CredentialKeyID: keyID,
		RawHeaders: map[string]string{
			HeaderKeyID:            keyID,
			HeaderEventID:          eventID,
			HeaderTimestamp:        formatTimestamp(timestamp),
			HeaderSignatureVersion: "v1",
		},
		RawBody:    append([]byte(nil), body...),
		BodySHA256: hex.EncodeToString(bodyDigest[:]),
	})
	if err != nil {
		http.Error(response, "webhook inbox unavailable", http.StatusServiceUnavailable)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	if created {
		_, _ = response.Write([]byte(`{"received":true,"duplicate":false}`))
		return
	}
	_, _ = response.Write([]byte(`{"received":true,"duplicate":true}`))
}

func formatTimestamp(timestamp int64) string { return strconv.FormatInt(timestamp, 10) }
