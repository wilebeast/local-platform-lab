package auth

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestTOTPBindingRequiresSecondFactorBeforeIssuingTokens(t *testing.T) {
	service := testService(t)
	service.now = func() time.Time { return time.Unix(1_800_000_000, 0) }

	setup, err := service.TOTPOptions(context.Background(), "local-user")
	if err != nil {
		t.Fatal(err)
	}
	secret := secretFromURI(t, setup.OTPAuthURI)
	code, err := totp.GenerateCode(secret, service.now())
	if err != nil {
		t.Fatal(err)
	}
	recoveryCodes, err := service.ConfirmTOTP(context.Background(), "local-user", setup.SetupToken, code)
	if err != nil {
		t.Fatal(err)
	}
	if len(recoveryCodes) != 10 {
		t.Fatalf("expected 10 recovery codes, got %d", len(recoveryCodes))
	}
	if _, err := service.Login(context.Background(), "demo@local.test", "local-demo-password"); !errors.Is(err, ErrSecondFactorRequired) {
		t.Fatalf("legacy login RPC bypassed enabled TOTP: %v", err)
	}

	preparation, err := service.PrepareLogin(context.Background(), "demo@local.test", "local-demo-password")
	if err != nil {
		t.Fatal(err)
	}
	if !preparation.SecondFactorRequired || preparation.AuthSessionToken == "" || preparation.TokenPair.AccessToken != "" {
		t.Fatalf("expected a second-factor challenge without tokens, got %+v", preparation)
	}

	pair, err := service.VerifyLogin2FA(context.Background(), preparation.AuthSessionToken, code)
	if err != nil {
		t.Fatal(err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatalf("expected tokens after valid TOTP, got %+v", pair)
	}
}

func TestRecoveryCodeCanOnlyBeUsedOnce(t *testing.T) {
	service := testService(t)
	service.now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	setup, err := service.TOTPOptions(context.Background(), "local-user")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secretFromURI(t, setup.OTPAuthURI), service.now())
	if err != nil {
		t.Fatal(err)
	}
	recoveryCodes, err := service.ConfirmTOTP(context.Background(), "local-user", setup.SetupToken, code)
	if err != nil {
		t.Fatal(err)
	}

	first, _ := service.PrepareLogin(context.Background(), "demo@local.test", "local-demo-password")
	if _, err := service.VerifyLogin2FA(context.Background(), first.AuthSessionToken, recoveryCodes[0]); err != nil {
		t.Fatalf("first recovery-code use failed: %v", err)
	}
	second, _ := service.PrepareLogin(context.Background(), "demo@local.test", "local-demo-password")
	if _, err := service.VerifyLogin2FA(context.Background(), second.AuthSessionToken, recoveryCodes[0]); err == nil {
		t.Fatal("expected a consumed recovery code to be rejected")
	}
}

func secretFromURI(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	secret := parsed.Query().Get("secret")
	if secret == "" {
		t.Fatalf("otpauth URI has no secret: %s", raw)
	}
	return secret
}
