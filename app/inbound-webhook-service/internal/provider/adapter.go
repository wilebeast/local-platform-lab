package provider

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
)

var ErrInvalidSignature = errors.New("invalid provider signature")

type SignedRequest struct {
	Timestamp int64
	EventID   string
	Body      []byte
	Signature string
}

type Credential struct {
	KeyID            string
	HMACSecret       []byte
	Ed25519PublicKey ed25519.PublicKey
}

type Adapter interface {
	Verify(request SignedRequest, credential Credential) error
}

type HMACAdapter struct{}

func (HMACAdapter) Verify(request SignedRequest, credential Credential) error {
	provided, err := hex.DecodeString(request.Signature)
	if err != nil || len(credential.HMACSecret) == 0 {
		return ErrInvalidSignature
	}
	mac := hmac.New(sha256.New, credential.HMACSecret)
	_, _ = mac.Write(CanonicalPayload(request))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return ErrInvalidSignature
	}
	return nil
}

type Ed25519Adapter struct{}

func (Ed25519Adapter) Verify(request SignedRequest, credential Credential) error {
	signature, err := base64.StdEncoding.DecodeString(request.Signature)
	if err != nil || len(credential.Ed25519PublicKey) != ed25519.PublicKeySize {
		return ErrInvalidSignature
	}
	if !ed25519.Verify(credential.Ed25519PublicKey, CanonicalPayload(request), signature) {
		return ErrInvalidSignature
	}
	return nil
}

func CanonicalPayload(request SignedRequest) []byte {
	prefix := strconv.FormatInt(request.Timestamp, 10) + "." + request.EventID + "."
	payload := make([]byte, 0, len(prefix)+len(request.Body))
	payload = append(payload, prefix...)
	payload = append(payload, request.Body...)
	return payload
}
