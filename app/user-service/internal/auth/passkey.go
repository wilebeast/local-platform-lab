package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const passkeyCeremonyTTL = 5 * time.Minute

var ErrPasskeyNotConfigured = errors.New("passkey is not configured")
var ErrInvalidPasskeyCeremony = errors.New("invalid passkey ceremony")

type PasskeyCeremony struct {
	UserID   string               `json:"userId"`
	UserName string               `json:"userName"`
	Purpose  string               `json:"purpose"`
	Session  webauthn.SessionData `json:"session"`
}

type PasskeyStore interface {
	PutPasskeyCeremony(context.Context, string, PasskeyCeremony, time.Duration) error
	ConsumePasskeyCeremony(context.Context, string) (PasskeyCeremony, error)
	PutPasskeyCredential(context.Context, PasskeyCredential) error
	GetPasskeyCredential(context.Context, []byte) (PasskeyCredential, error)
}

type PasskeyRegistrationStart struct {
	CeremonyID  string
	OptionsJSON []byte
}

type PasskeyLoginStart struct {
	CeremonyID  string
	OptionsJSON []byte
}

type PasskeyCredential struct {
	UserID     string              `json:"userId"`
	UserName   string              `json:"userName"`
	Credential webauthn.Credential `json:"credential"`
	CreatedAt  int64               `json:"createdAt"`
	LastUsedAt int64               `json:"lastUsedAt"`
	Status     string              `json:"status"`
}

type passkeyProvider interface {
	BeginRegistration(webauthn.User, ...webauthn.RegistrationOption) (*protocol.CredentialCreation, *webauthn.SessionData, error)
	BeginDiscoverableLogin(...webauthn.LoginOption) (*protocol.CredentialAssertion, *webauthn.SessionData, error)
	CreateCredential(webauthn.User, webauthn.SessionData, *protocol.ParsedCredentialCreationData) (*webauthn.Credential, error)
	ValidatePasskeyLogin(webauthn.DiscoverableUserHandler, webauthn.SessionData, *protocol.ParsedCredentialAssertionData) (webauthn.User, *webauthn.Credential, error)
}

type passkeyUser struct {
	id          []byte
	name        string
	credentials []webauthn.Credential
}

func (u passkeyUser) WebAuthnID() []byte                         { return u.id }
func (u passkeyUser) WebAuthnName() string                       { return u.name }
func (u passkeyUser) WebAuthnDisplayName() string                { return u.name }
func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

func newPasskeyProvider(config Config) (*webauthn.WebAuthn, error) {
	if config.PasskeyRPID == "" && config.PasskeyRPName == "" && len(config.PasskeyOrigins) == 0 {
		return nil, nil
	}
	if config.PasskeyRPID == "" || config.PasskeyRPName == "" || len(config.PasskeyOrigins) == 0 {
		return nil, errors.New("invalid passkey configuration")
	}
	return webauthn.New(&webauthn.Config{
		RPID:                  config.PasskeyRPID,
		RPDisplayName:         config.PasskeyRPName,
		RPOrigins:             config.PasskeyOrigins,
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		},
		Timeouts: webauthn.TimeoutsConfig{
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: passkeyCeremonyTTL, TimeoutUVD: passkeyCeremonyTTL},
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: passkeyCeremonyTTL, TimeoutUVD: passkeyCeremonyTTL},
		},
	})
}

func (s *Service) BeginPasskeyRegistration(ctx context.Context, userID, userName string) (PasskeyRegistrationStart, error) {
	if s.passkey == nil || s.passkeyStore == nil {
		return PasskeyRegistrationStart{}, ErrPasskeyNotConfigured
	}
	if userID == "" || userName == "" {
		return PasskeyRegistrationStart{}, errors.New("passkey user id and name are required")
	}
	handle := sha256.Sum256([]byte(userID))
	creation, session, err := s.passkey.BeginRegistration(passkeyUser{id: handle[:], name: userName})
	if err != nil {
		return PasskeyRegistrationStart{}, err
	}
	optionsJSON, err := json.Marshal(creation)
	if err != nil {
		return PasskeyRegistrationStart{}, err
	}
	ceremonyID, err := randomToken()
	if err != nil {
		return PasskeyRegistrationStart{}, err
	}
	ceremony := PasskeyCeremony{UserID: userID, UserName: userName, Purpose: "registration", Session: *session}
	if err := s.passkeyStore.PutPasskeyCeremony(ctx, ceremonyID, ceremony, passkeyCeremonyTTL); err != nil {
		return PasskeyRegistrationStart{}, err
	}
	return PasskeyRegistrationStart{CeremonyID: ceremonyID, OptionsJSON: optionsJSON}, nil
}

func (s *Service) FinishPasskeyRegistration(ctx context.Context, ceremonyID string, responseJSON []byte) (PasskeyCredential, error) {
	if s.passkey == nil || s.passkeyStore == nil {
		return PasskeyCredential{}, ErrPasskeyNotConfigured
	}
	if ceremonyID == "" || len(responseJSON) == 0 {
		return PasskeyCredential{}, ErrInvalidPasskeyCeremony
	}
	ceremony, err := s.passkeyStore.ConsumePasskeyCeremony(ctx, ceremonyID)
	if err != nil || ceremony.Purpose != "registration" || ceremony.UserID == "" {
		return PasskeyCredential{}, ErrInvalidPasskeyCeremony
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(responseJSON)
	if err != nil {
		return PasskeyCredential{}, ErrInvalidPasskeyCeremony
	}
	credential, err := s.passkey.CreateCredential(passkeyUser{id: ceremony.Session.UserID, name: ceremony.UserName}, ceremony.Session, parsed)
	if err != nil {
		return PasskeyCredential{}, ErrInvalidPasskeyCeremony
	}
	now := s.now().Unix()
	registered := PasskeyCredential{
		UserID: ceremony.UserID, UserName: ceremony.UserName, Credential: *credential,
		CreatedAt: now, LastUsedAt: now, Status: "active",
	}
	if err := s.passkeyStore.PutPasskeyCredential(ctx, registered); err != nil {
		return PasskeyCredential{}, err
	}
	return registered, nil
}

func (s *Service) BeginPasskeyLogin(ctx context.Context) (PasskeyLoginStart, error) {
	if s.passkey == nil || s.passkeyStore == nil {
		return PasskeyLoginStart{}, ErrPasskeyNotConfigured
	}
	assertion, session, err := s.passkey.BeginDiscoverableLogin()
	if err != nil {
		return PasskeyLoginStart{}, err
	}
	optionsJSON, err := json.Marshal(assertion)
	if err != nil {
		return PasskeyLoginStart{}, err
	}
	ceremonyID, err := randomToken()
	if err != nil {
		return PasskeyLoginStart{}, err
	}
	ceremony := PasskeyCeremony{Purpose: "login", Session: *session}
	if err := s.passkeyStore.PutPasskeyCeremony(ctx, ceremonyID, ceremony, passkeyCeremonyTTL); err != nil {
		return PasskeyLoginStart{}, err
	}
	return PasskeyLoginStart{CeremonyID: ceremonyID, OptionsJSON: optionsJSON}, nil
}

func (s *Service) FinishPasskeyLogin(ctx context.Context, ceremonyID string, responseJSON []byte) (TokenPair, error) {
	if s.passkey == nil || s.passkeyStore == nil {
		return TokenPair{}, ErrPasskeyNotConfigured
	}
	if ceremonyID == "" || len(responseJSON) == 0 {
		return TokenPair{}, ErrInvalidPasskeyCeremony
	}
	ceremony, err := s.passkeyStore.ConsumePasskeyCeremony(ctx, ceremonyID)
	if err != nil || ceremony.Purpose != "login" {
		return TokenPair{}, ErrInvalidPasskeyCeremony
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(responseJSON)
	if err != nil {
		return TokenPair{}, ErrInvalidPasskeyCeremony
	}
	var record PasskeyCredential
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		loaded, loadErr := s.passkeyStore.GetPasskeyCredential(ctx, rawID)
		if loadErr != nil || loaded.Status != "active" {
			return nil, ErrInvalidPasskeyCeremony
		}
		expectedHandle := sha256.Sum256([]byte(loaded.UserID))
		if !bytes.Equal(expectedHandle[:], userHandle) {
			return nil, ErrInvalidPasskeyCeremony
		}
		record = loaded
		return passkeyUser{id: expectedHandle[:], name: loaded.UserName, credentials: []webauthn.Credential{loaded.Credential}}, nil
	}
	_, credential, err := s.passkey.ValidatePasskeyLogin(handler, ceremony.Session, parsed)
	if err != nil || record.UserID == "" {
		return TokenPair{}, fmt.Errorf("%w: %v", ErrInvalidPasskeyCeremony, err)
	}
	record.Credential = *credential
	record.LastUsedAt = s.now().Unix()
	if err := s.passkeyStore.PutPasskeyCredential(ctx, record); err != nil {
		return TokenPair{}, err
	}
	return s.issue(ctx, record.UserID, "passkey")
}
