package accountcontext

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	metadataContextKey   = "x-account-context"
	metadataSignatureKey = "x-account-context-signature"
)

var ErrInvalidContext = errors.New("invalid account context")

type Context struct {
	UserID            string   `json:"user_id"`
	SessionID         string   `json:"session_id"`
	AccountID         string   `json:"account_id"`
	AccountUID        string   `json:"account_uid"`
	AccountType       string   `json:"account_type"`
	OrganizationID    string   `json:"organization_id,omitempty"`
	MemberID          string   `json:"member_id,omitempty"`
	RoleIDs           []string `json:"role_ids,omitempty"`
	PermissionVersion int64    `json:"permission_version"`
	SelectedAt        int64    `json:"selected_at"`
	IssuedAt          int64    `json:"issued_at"`
}

type Signer struct {
	secret []byte
	now    func() time.Time
}

func NewSigner(secret string) (*Signer, error) {
	if len(secret) < 32 {
		return nil, errors.New("account context signing key must be at least 32 bytes")
	}
	return &Signer{secret: []byte(secret), now: time.Now}, nil
}

func (s *Signer) Attach(ctx context.Context, value Context) (context.Context, error) {
	value.IssuedAt = s.now().Unix()
	if err := validate(value); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write(payload)
	return metadata.AppendToOutgoingContext(ctx,
		metadataContextKey, base64.RawURLEncoding.EncodeToString(payload),
		metadataSignatureKey, base64.RawURLEncoding.EncodeToString(mac.Sum(nil)),
	), nil
}

func UnaryServerInterceptor(secret string, maxAge time.Duration, publicMethods ...string) (grpc.UnaryServerInterceptor, error) {
	signer, err := NewSigner(secret)
	if err != nil {
		return nil, err
	}
	public := make(map[string]struct{}, len(publicMethods))
	for _, method := range publicMethods {
		public[method] = struct{}{}
	}
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if _, ok := public[info.FullMethod]; ok {
			return handler(ctx, request)
		}
		value, err := signer.parseIncoming(ctx, maxAge)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid account context")
		}
		return handler(context.WithValue(ctx, contextKey{}, value), request)
	}, nil
}

func (s *Signer) parseIncoming(ctx context.Context, maxAge time.Duration) (Context, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok || len(md.Get(metadataContextKey)) != 1 || len(md.Get(metadataSignatureKey)) != 1 {
		return Context{}, ErrInvalidContext
	}
	payload, err := base64.RawURLEncoding.DecodeString(md.Get(metadataContextKey)[0])
	if err != nil {
		return Context{}, ErrInvalidContext
	}
	signature, err := base64.RawURLEncoding.DecodeString(md.Get(metadataSignatureKey)[0])
	if err != nil {
		return Context{}, ErrInvalidContext
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return Context{}, ErrInvalidContext
	}
	var value Context
	if json.Unmarshal(payload, &value) != nil || validate(value) != nil {
		return Context{}, ErrInvalidContext
	}
	issuedAt := time.Unix(value.IssuedAt, 0)
	if maxAge > 0 && (s.now().Sub(issuedAt) > maxAge || issuedAt.Sub(s.now()) > maxAge) {
		return Context{}, ErrInvalidContext
	}
	return value, nil
}

func validate(value Context) error {
	if value.UserID == "" || value.SessionID == "" || value.AccountID == "" || value.AccountUID == "" || value.AccountType == "" || value.PermissionVersion <= 0 || value.SelectedAt <= 0 || value.IssuedAt <= 0 {
		return ErrInvalidContext
	}
	return nil
}

type contextKey struct{}

func FromContext(ctx context.Context) (Context, bool) {
	value, ok := ctx.Value(contextKey{}).(Context)
	return value, ok
}
