package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

var (
	ErrSecondFactorRequired = errors.New("second factor required")
	ErrInvalidSecondFactor  = errors.New("invalid second factor")
	ErrTOTPAlreadyBound     = errors.New("totp already bound")
	ErrTOTPNotBound         = errors.New("totp not bound")
)

const (
	totpSetupTTL       = 10 * time.Minute
	loginSessionTTL    = 5 * time.Minute
	recoveryCodeCount  = 10
	recoveryCodeLength = 10
)

type LoginPreparation struct {
	SecondFactorRequired bool
	AuthSessionToken     string
	TokenPair            TokenPair
}

type TOTPSetup struct {
	SetupToken string
	OTPAuthURI string
}

func (s *Service) PrepareLogin(ctx context.Context, email, password string) (LoginPreparation, error) {
	if !s.validCredentials(email, password) {
		return LoginPreparation{}, ErrInvalidCredentials
	}
	secret, err := s.loadTOTPSecret(ctx, s.config.DemoUserID)
	if err != nil {
		return LoginPreparation{}, err
	}
	if secret == "" {
		pair, err := s.issue(ctx, s.config.DemoUserID)
		return LoginPreparation{TokenPair: pair}, err
	}
	token, err := randomToken()
	if err != nil {
		return LoginPreparation{}, err
	}
	if err := s.factors.PutLoginSession(ctx, tokenHash(token), s.config.DemoUserID, loginSessionTTL); err != nil {
		return LoginPreparation{}, err
	}
	return LoginPreparation{SecondFactorRequired: true, AuthSessionToken: token}, nil
}

func (s *Service) VerifyLogin2FA(ctx context.Context, sessionToken, code string) (TokenPair, error) {
	userID, err := s.factors.ConsumeLoginSession(ctx, tokenHash(sessionToken))
	if err != nil || userID == "" {
		return TokenPair{}, ErrInvalidSecondFactor
	}
	valid, err := s.verifySecondFactor(ctx, userID, code)
	if err != nil {
		return TokenPair{}, err
	}
	if !valid {
		return TokenPair{}, ErrInvalidSecondFactor
	}
	return s.issue(ctx, userID)
}

func (s *Service) TOTPOptions(ctx context.Context, userID string) (TOTPSetup, error) {
	if userID != s.config.DemoUserID {
		return TOTPSetup{}, ErrInvalidCredentials
	}
	existing, err := s.loadTOTPSecret(ctx, userID)
	if err != nil {
		return TOTPSetup{}, err
	}
	if existing != "" {
		return TOTPSetup{}, ErrTOTPAlreadyBound
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Wildbeast Local", AccountName: s.config.DemoEmail})
	if err != nil {
		return TOTPSetup{}, err
	}
	encrypted, err := s.encryptSecret(key.Secret())
	if err != nil {
		return TOTPSetup{}, err
	}
	token, err := randomToken()
	if err != nil {
		return TOTPSetup{}, err
	}
	if err := s.factors.PutTOTPSetup(ctx, tokenHash(token), encrypted, totpSetupTTL); err != nil {
		return TOTPSetup{}, err
	}
	return TOTPSetup{SetupToken: token, OTPAuthURI: key.URL()}, nil
}

func (s *Service) ConfirmTOTP(ctx context.Context, userID, setupToken, code string) ([]string, error) {
	if userID != s.config.DemoUserID {
		return nil, ErrInvalidCredentials
	}
	encrypted, err := s.factors.ConsumeTOTPSetup(ctx, tokenHash(setupToken))
	if err != nil || encrypted == "" {
		return nil, ErrInvalidSecondFactor
	}
	secret, err := s.decryptSecret(encrypted)
	if err != nil {
		return nil, err
	}
	if !s.validTOTP(secret, code) {
		return nil, ErrInvalidSecondFactor
	}
	codes, digests, err := s.newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := s.factors.PutTOTPSecret(ctx, userID, encrypted); err != nil {
		return nil, err
	}
	if err := s.factors.ReplaceRecoveryDigests(ctx, userID, digests); err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *Service) UnbindTOTP(ctx context.Context, userID, password, code string) error {
	if userID != s.config.DemoUserID || !s.validCredentials(s.config.DemoEmail, password) {
		return ErrInvalidCredentials
	}
	valid, err := s.verifySecondFactor(ctx, userID, code)
	if err != nil {
		return err
	}
	if !valid {
		return ErrInvalidSecondFactor
	}
	return s.factors.DeleteTOTP(ctx, userID)
}

func (s *Service) RegenerateRecoveryCodes(ctx context.Context, userID, code string) ([]string, error) {
	valid, err := s.verifySecondFactor(ctx, userID, code)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, ErrInvalidSecondFactor
	}
	codes, digests, err := s.newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := s.factors.ReplaceRecoveryDigests(ctx, userID, digests); err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *Service) validCredentials(email, password string) bool {
	return hmac.Equal([]byte(email), []byte(s.config.DemoEmail)) && hmac.Equal([]byte(password), []byte(s.config.DemoPassword))
}

func (s *Service) verifySecondFactor(ctx context.Context, userID, code string) (bool, error) {
	secret, err := s.loadTOTPSecret(ctx, userID)
	if err != nil {
		return false, err
	}
	if secret == "" {
		return false, ErrTOTPNotBound
	}
	code = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	if s.validTOTP(secret, code) {
		return true, nil
	}
	return s.factors.ConsumeRecoveryDigest(ctx, userID, s.recoveryDigest(code))
}

func (s *Service) validTOTP(secret, code string) bool {
	valid, _ := totp.ValidateCustom(strings.TrimSpace(code), secret, s.now(), totp.ValidateOpts{Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	return valid
}

func (s *Service) loadTOTPSecret(ctx context.Context, userID string) (string, error) {
	encrypted, err := s.factors.GetTOTPSecret(ctx, userID)
	if err != nil || encrypted == "" {
		return "", err
	}
	return s.decryptSecret(encrypted)
}

func (s *Service) encryptSecret(secret string) (string, error) {
	block, err := aes.NewCipher(s.encryptionKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(secret), nil)), nil
}

func (s *Service) decryptSecret(encoded string) (string, error) {
	value, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.encryptionKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(value) < gcm.NonceSize() {
		return "", ErrInvalidSecondFactor
	}
	plain, err := gcm.Open(nil, value[:gcm.NonceSize()], value[gcm.NonceSize():], nil)
	return string(plain), err
}

func (s *Service) encryptionKey() []byte {
	sum := sha256.Sum256([]byte(s.config.TOTPEncryptionKey))
	return sum[:]
}

func (s *Service) newRecoveryCodes() ([]string, []string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	codes := make([]string, recoveryCodeCount)
	digests := make([]string, recoveryCodeCount)
	limit := big.NewInt(int64(len(alphabet)))
	for i := range codes {
		value := make([]byte, recoveryCodeLength)
		for j := range value {
			n, err := rand.Int(rand.Reader, limit)
			if err != nil {
				return nil, nil, err
			}
			value[j] = alphabet[n.Int64()]
		}
		codes[i] = string(value[:5]) + "-" + string(value[5:])
		digests[i] = s.recoveryDigest(string(value))
	}
	return codes, digests, nil
}

func (s *Service) recoveryDigest(code string) string {
	mac := hmac.New(sha256.New, []byte(s.config.RecoveryCodePepper))
	_, _ = mac.Write([]byte(code))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
