package authn

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/redis/go-redis/v9"
)

var ErrInvalidAccessToken = errors.New("invalid access token")

type Actor struct {
	UserID          string
	SessionID       string
	TokenID         string
	UserVersion     int64
	AuthMethods     []string
	AuthenticatedAt int64
}

type sessionRecord struct {
	ID              string
	UserID          string
	UserVersion     int64
	AuthMethods     []string
	AuthenticatedAt int64
	Status          string
}

type Validator struct {
	publicKey ed25519.PublicKey
	keyID     string
	issuer    string
	audience  string
	redis     *redis.Client
	now       func() time.Time
}

func NewValidator(publicKeyB64, keyID, issuer, audience, redisAddress string) (*Validator, error) {
	der, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil {
		return nil, fmt.Errorf("decode JWT public key: %w", err)
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse JWT public key: %w", err)
	}
	publicKey, ok := parsed.(ed25519.PublicKey)
	if !ok || keyID == "" || issuer == "" || audience == "" || redisAddress == "" {
		return nil, errors.New("invalid JWT validator configuration")
	}
	return &Validator{
		publicKey: publicKey, keyID: keyID, issuer: issuer, audience: audience,
		redis: redis.NewClient(&redis.Options{Addr: redisAddress}), now: time.Now,
	}, nil
}

func (v *Validator) Ping(ctx context.Context) error { return v.redis.Ping(ctx).Err() }
func (v *Validator) Close() error                   { return v.redis.Close() }

func (v *Validator) ValidateRequest(ctx context.Context, request *http.Request) (Actor, error) {
	raw, err := tokenFromRequest(request)
	if err != nil {
		return Actor{}, err
	}
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodEdDSA || token.Header["alg"] != "EdDSA" || token.Header["kid"] != v.keyID {
			return nil, ErrInvalidAccessToken
		}
		return v.publicKey, nil
	}, jwt.WithValidMethods([]string{"EdDSA"}))
	if err != nil || !token.Valid || !claims.VerifyIssuer(v.issuer, true) || !claims.VerifyAudience(v.audience, true) || !claims.VerifyExpiresAt(v.now().Unix(), true) {
		return Actor{}, ErrInvalidAccessToken
	}
	actor, err := actorFromClaims(claims)
	if err != nil {
		return Actor{}, ErrInvalidAccessToken
	}
	value, err := v.redis.Get(ctx, "auth:session:"+actor.SessionID).Bytes()
	if err != nil {
		return Actor{}, ErrInvalidAccessToken
	}
	var session sessionRecord
	if json.Unmarshal(value, &session) != nil || session.Status != "active" || session.ID != actor.SessionID || session.UserID != actor.UserID || session.UserVersion != actor.UserVersion {
		return Actor{}, ErrInvalidAccessToken
	}
	version, err := v.redis.Get(ctx, "auth:user-version:"+actor.UserID).Int64()
	if err != nil || version != actor.UserVersion {
		return Actor{}, ErrInvalidAccessToken
	}
	return actor, nil
}

func (v *Validator) JWKS() map[string]any {
	return map[string]any{"keys": []map[string]any{{
		"kty": "OKP", "crv": "Ed25519", "use": "sig", "alg": "EdDSA", "kid": v.keyID,
		"x": base64.RawURLEncoding.EncodeToString(v.publicKey),
	}}}
}

func tokenFromRequest(request *http.Request) (string, error) {
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	bearer := ""
	if len(authorization) > 7 && strings.EqualFold(authorization[:7], "Bearer ") {
		bearer = strings.TrimSpace(authorization[7:])
	}
	cookie := ""
	if value, err := request.Cookie("access_token"); err == nil {
		cookie = value.Value
	}
	if bearer != "" && cookie != "" && bearer != cookie {
		return "", ErrInvalidAccessToken
	}
	if bearer != "" {
		return bearer, nil
	}
	if cookie != "" {
		return cookie, nil
	}
	return "", ErrInvalidAccessToken
}

func actorFromClaims(claims jwt.MapClaims) (Actor, error) {
	userID, _ := claims["sub"].(string)
	sid, _ := claims["sid"].(string)
	jti, _ := claims["jti"].(string)
	version, ok := numberClaim(claims["ver"])
	authTime, authTimeOK := numberClaim(claims["auth_time"])
	methods := make([]string, 0)
	if values, ok := claims["amr"].([]any); ok {
		for _, value := range values {
			if method, ok := value.(string); ok {
				methods = append(methods, method)
			}
		}
	}
	if userID == "" || sid == "" || jti == "" || !ok || !authTimeOK {
		return Actor{}, ErrInvalidAccessToken
	}
	return Actor{UserID: userID, SessionID: sid, TokenID: jti, UserVersion: version, AuthMethods: methods, AuthenticatedAt: authTime}, nil
}

func numberClaim(value any) (int64, bool) {
	switch number := value.(type) {
	case float64:
		return int64(number), number == float64(int64(number))
	case json.Number:
		parsed, err := number.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}
