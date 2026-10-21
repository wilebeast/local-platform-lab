package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/golang-jwt/jwt/v4"
)

func TestBeginPasskeyRegistrationReturnsBrowserOptions(t *testing.T) {
	store := newMemoryStore()
	privateKey := ed25519.NewKeyFromSeed([]byte("01234567890123456789012345678901"))
	service, err := NewService(Config{
		JWTPrivateKey: privateKey, JWTKeyID: "test-key-1", Issuer: "local-platform-lab", Audience: "api-bff",
		AccessTTL: 30 * time.Minute, RefreshTTL: 7 * 24 * time.Hour,
		DemoEmail: "demo@local.test", DemoPassword: "local-demo-password", DemoUserID: "local-user",
		TOTPEncryptionKey: "01234567890123456789012345678901", RecoveryCodePepper: "abcdefghijklmnopqrstuvwxyz123456",
		PasskeyRPID: "localhost", PasskeyRPName: "Local Platform Lab", PasskeyOrigins: []string{"https://localhost:18443"},
	}, store)
	if err != nil {
		t.Fatal(err)
	}

	start, err := service.BeginPasskeyRegistration(context.Background(), "local-user", "demo@local.test")
	if err != nil {
		t.Fatal(err)
	}
	if start.CeremonyID == "" {
		t.Fatal("registration ceremony id is empty")
	}

	var options struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			RP        struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"rp"`
			User struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				DisplayName string `json:"displayName"`
			} `json:"user"`
			AuthenticatorSelection struct {
				ResidentKey      string `json:"residentKey"`
				UserVerification string `json:"userVerification"`
			} `json:"authenticatorSelection"`
			Attestation string `json:"attestation"`
			Timeout     int    `json:"timeout"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(start.OptionsJSON, &options); err != nil {
		t.Fatalf("options are not valid JSON: %v", err)
	}
	publicKey := options.PublicKey
	if publicKey.Challenge == "" || publicKey.RP.ID != "localhost" || publicKey.RP.Name != "Local Platform Lab" {
		t.Fatalf("unexpected RP options: %+v", publicKey)
	}
	if publicKey.User.ID == "" || publicKey.User.Name != "demo@local.test" || publicKey.User.DisplayName != "demo@local.test" {
		t.Fatalf("unexpected user options: %+v", publicKey.User)
	}
	if publicKey.AuthenticatorSelection.ResidentKey != "required" || publicKey.AuthenticatorSelection.UserVerification != "required" {
		t.Fatalf("passkey requirements are not enforced: %+v", publicKey.AuthenticatorSelection)
	}
	if publicKey.Attestation != "none" || publicKey.Timeout != 300000 {
		t.Fatalf("unexpected ceremony policy: attestation=%q timeout=%d", publicKey.Attestation, publicKey.Timeout)
	}
}

func TestFinishPasskeyRegistrationStoresCredentialAndRejectsReplay(t *testing.T) {
	store := newMemoryStore()
	privateKey := ed25519.NewKeyFromSeed([]byte("01234567890123456789012345678901"))
	service, err := NewService(Config{
		JWTPrivateKey: privateKey, JWTKeyID: "test-key-1", Issuer: "local-platform-lab", Audience: "api-bff",
		AccessTTL: 30 * time.Minute, RefreshTTL: 7 * 24 * time.Hour,
		DemoEmail: "demo@local.test", DemoPassword: "local-demo-password", DemoUserID: "local-user",
		TOTPEncryptionKey: "01234567890123456789012345678901", RecoveryCodePepper: "abcdefghijklmnopqrstuvwxyz123456",
		PasskeyRPID: "example.org", PasskeyRPName: "Local Platform Lab", PasskeyOrigins: []string{"https://example.org"},
	}, store)
	if err != nil {
		t.Fatal(err)
	}

	body, challenge, credentialID := registrationSpecVector(t)
	handle := sha256.Sum256([]byte("local-user"))
	store.passkeyCeremonies["ceremony-1"] = PasskeyCeremony{
		UserID: "local-user", UserName: "demo@local.test", Purpose: "registration",
		Session: webauthn.SessionData{
			Challenge: challenge, RelyingPartyID: "example.org", Origin: "https://example.org", UserID: handle[:],
			Expires: time.Now().Add(5 * time.Minute), UserVerification: protocol.VerificationRequired,
			CredParams: []protocol.CredentialParameter{{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgES256}},
		},
	}

	registered, err := service.FinishPasskeyRegistration(context.Background(), "ceremony-1", body)
	if err != nil {
		t.Fatal(err)
	}
	if registered.UserID != "local-user" || !strings.EqualFold(hex.EncodeToString(registered.Credential.ID), hex.EncodeToString(credentialID)) {
		t.Fatalf("unexpected registered credential: %+v", registered)
	}
	if _, err := service.FinishPasskeyRegistration(context.Background(), "ceremony-1", body); !errors.Is(err, ErrInvalidPasskeyCeremony) {
		t.Fatalf("expected one-time ceremony rejection, got %v", err)
	}
}

func TestBeginPasskeyLoginReturnsDiscoverableOptions(t *testing.T) {
	store := newMemoryStore()
	privateKey := ed25519.NewKeyFromSeed([]byte("01234567890123456789012345678901"))
	service, err := NewService(Config{
		JWTPrivateKey: privateKey, JWTKeyID: "test-key-1", Issuer: "local-platform-lab", Audience: "api-bff",
		AccessTTL: 30 * time.Minute, RefreshTTL: 7 * 24 * time.Hour,
		DemoEmail: "demo@local.test", DemoPassword: "local-demo-password", DemoUserID: "local-user",
		TOTPEncryptionKey: "01234567890123456789012345678901", RecoveryCodePepper: "abcdefghijklmnopqrstuvwxyz123456",
		PasskeyRPID: "localhost", PasskeyRPName: "Local Platform Lab", PasskeyOrigins: []string{"https://localhost:18443"},
	}, store)
	if err != nil {
		t.Fatal(err)
	}

	start, err := service.BeginPasskeyLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if start.CeremonyID == "" {
		t.Fatal("login ceremony id is empty")
	}
	var options struct {
		PublicKey struct {
			Challenge        string `json:"challenge"`
			RPID             string `json:"rpId"`
			UserVerification string `json:"userVerification"`
			Timeout          int    `json:"timeout"`
			AllowCredentials []any  `json:"allowCredentials"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(start.OptionsJSON, &options); err != nil {
		t.Fatal(err)
	}
	publicKey := options.PublicKey
	if publicKey.Challenge == "" || publicKey.RPID != "localhost" || publicKey.UserVerification != "required" || publicKey.Timeout != 300000 {
		t.Fatalf("unexpected login options: %+v", publicKey)
	}
	if len(publicKey.AllowCredentials) != 0 {
		t.Fatalf("discoverable login must not restrict credential ids: %+v", publicKey.AllowCredentials)
	}
}

func TestFinishPasskeyLoginIssuesPasskeySessionAndRejectsReplay(t *testing.T) {
	store := newMemoryStore()
	privateKey := ed25519.NewKeyFromSeed([]byte("01234567890123456789012345678901"))
	service, err := NewService(Config{
		JWTPrivateKey: privateKey, JWTKeyID: "test-key-1", Issuer: "local-platform-lab", Audience: "api-bff",
		AccessTTL: 30 * time.Minute, RefreshTTL: 7 * 24 * time.Hour,
		DemoEmail: "demo@local.test", DemoPassword: "local-demo-password", DemoUserID: "local-user",
		TOTPEncryptionKey: "01234567890123456789012345678901", RecoveryCodePepper: "abcdefghijklmnopqrstuvwxyz123456",
		PasskeyRPID: "example.org", PasskeyRPName: "Local Platform Lab", PasskeyOrigins: []string{"https://example.org"},
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	body, challenge, credentialID, publicKey := loginSpecVector(t)
	credential := PasskeyCredential{
		UserID: "local-user", UserName: "demo@local.test", Status: "active",
		Credential: webauthn.Credential{
			ID: credentialID, PublicKey: publicKey,
			Flags: webauthn.CredentialFlags{UserPresent: true, BackupEligible: true},
		},
	}
	if err := store.PutPasskeyCredential(context.Background(), credential); err != nil {
		t.Fatal(err)
	}
	store.passkeyCeremonies["login-1"] = PasskeyCeremony{
		Purpose: "login",
		Session: webauthn.SessionData{
			Challenge: challenge, RelyingPartyID: "example.org", Origin: "https://example.org",
			Expires: time.Now().Add(5 * time.Minute), UserVerification: protocol.VerificationDiscouraged,
		},
	}

	pair, err := service.FinishPasskeyLogin(context.Background(), "login-1", body)
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwt.Parse(pair.AccessToken, func(*jwt.Token) (any, error) { return privateKey.Public(), nil })
	if err != nil {
		t.Fatal(err)
	}
	claims := token.Claims.(jwt.MapClaims)
	amr, ok := claims["amr"].([]any)
	if !ok || len(amr) != 1 || amr[0] != "passkey" || claims["sub"] != "local-user" {
		t.Fatalf("unexpected passkey token claims: %+v", claims)
	}
	if _, err := service.FinishPasskeyLogin(context.Background(), "login-1", body); !errors.Is(err, ErrInvalidPasskeyCeremony) {
		t.Fatalf("expected replay rejection, got %v", err)
	}
}

func loginSpecVector(t *testing.T) (body []byte, challenge string, credentialID, publicKey []byte) {
	t.Helper()
	const (
		authenticatorDataHex = "bfabc37432958b063360d3ad6461c9c4735ae7f8edd46592a5e0f01452b2e4b51900000000"
		clientDataJSONHex    = "7b2274797065223a22776562617574686e2e676574222c226368616c6c656e6765223a224f63446e55685158756c5455506f334a5558543049393770767a7a59425039745a63685879617630314167222c226f726967696e223a2268747470733a2f2f6578616d706c652e6f7267222c2263726f73734f726967696e223a66616c73657d"
		signatureHex         = "3046022100f50a4e2e4409249c4a853ba361282f09841df4dd4547a13a87780218deffcd380221008480ac0f0b93538174f575bf11a1dd5d78c6e486013f937295ea13653e331e87"
		credentialIDHex      = "f91f391db4c9b2fde0ea70189cba3fb63f579ba6122b33ad94ff3ec330084be4"
		challengeHex         = "39c0e7521417ba54d43e8dc95174f423dee9bf3cd804ff6d65c857c9abf4d408"
		credentialPubKeyHex  = "a5010203262001215820afefa16f97ca9b2d23eb86ccb64098d20db90856062eb249c33a9b672f26df61225820930a56b87a2fca66334b03458abf879717c12cc68ed73290af2e2664796b9220"
	)
	decode := func(value string) []byte {
		decoded, err := hex.DecodeString(value)
		if err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	credentialID, publicKey = decode(credentialIDHex), decode(credentialPubKeyHex)
	id := base64.RawURLEncoding.EncodeToString(credentialID)
	userHandle := sha256.Sum256([]byte("local-user"))
	response := map[string]any{
		"id": id, "rawId": id, "type": "public-key",
		"response": map[string]any{
			"authenticatorData": base64.RawURLEncoding.EncodeToString(decode(authenticatorDataHex)),
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(decode(clientDataJSONHex)),
			"signature":         base64.RawURLEncoding.EncodeToString(decode(signatureHex)),
			"userHandle":        base64.RawURLEncoding.EncodeToString(userHandle[:]),
		},
	}
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return body, base64.RawURLEncoding.EncodeToString(decode(challengeHex)), credentialID, publicKey
}

func registrationSpecVector(t *testing.T) ([]byte, string, []byte) {
	t.Helper()
	const (
		attestationObjectHex = "a363666d74646e6f6e656761747453746d74a068617574684461746158a4bfabc37432958b063360d3ad6461c9c4735ae7f8edd46592a5e0f01452b2e4b559000000008446ccb9ab1db374750b2367ff6f3a1f0020f91f391db4c9b2fde0ea70189cba3fb63f579ba6122b33ad94ff3ec330084be4a5010203262001215820afefa16f97ca9b2d23eb86ccb64098d20db90856062eb249c33a9b672f26df61225820930a56b87a2fca66334b03458abf879717c12cc68ed73290af2e2664796b9220"
		clientDataJSONHex    = "7b2274797065223a22776562617574686e2e637265617465222c226368616c6c656e6765223a22414d4d507434557878475453746e63647134313759447742466938767049612d7077386f4f755657345441222c226f726967696e223a2268747470733a2f2f6578616d706c652e6f7267222c2263726f73734f726967696e223a66616c73652c22657874726144617461223a22636c69656e74446174614a534f4e206d617920626520657874656e6465642077697468206164646974696f6e616c206669656c647320696e20746865206675747572652c207375636820617320746869733a20426b5165446a646354427258426941774a544c453551227d"
		credentialIDHex      = "f91f391db4c9b2fde0ea70189cba3fb63f579ba6122b33ad94ff3ec330084be4"
		challengeHex         = "00c30fb78531c464d2b6771dab8d7b603c01162f2fa486bea70f283ae556e130"
		rpIDHashHex          = "bfabc37432958b063360d3ad6461c9c4735ae7f8edd46592a5e0f01452b2e4b5"
	)
	decode := func(value string) []byte {
		decoded, err := hex.DecodeString(value)
		if err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	credentialID := decode(credentialIDHex)
	// The W3C none-attestation vector is unsigned, so enabling the UV flag does not invalidate it.
	attestation := strings.Replace(attestationObjectHex, rpIDHashHex+"59", rpIDHashHex+"5d", 1)
	id := base64.RawURLEncoding.EncodeToString(credentialID)
	response := map[string]any{
		"id": id, "rawId": id, "type": "public-key",
		"response": map[string]any{
			"attestationObject": base64.RawURLEncoding.EncodeToString(decode(attestation)),
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(decode(clientDataJSONHex)),
		},
	}
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return body, base64.RawURLEncoding.EncodeToString(decode(challengeHex)), credentialID
}
