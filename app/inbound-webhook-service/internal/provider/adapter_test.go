package provider

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"
)

func TestHMACAdapterVerifiesKnownSignatureAndRejectsTampering(t *testing.T) {
	request := SignedRequest{
		Timestamp: 1_800_000_000,
		EventID:   "evt-demo-1",
		Body:      []byte(`{"id":"evt-demo-1","type":"payment.settled"}`),
		Signature: "1ad153c291520f09f74ac4572687dd24f1ad061c936790517ff22ed419469053",
	}
	adapter := HMACAdapter{}
	credential := Credential{KeyID: "payments-v1", HMACSecret: []byte("demo-hmac-secret")}

	if err := adapter.Verify(request, credential); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	request.Body = []byte(`{"id":"evt-demo-1","type":"payment.failed"}`)
	if err := adapter.Verify(request, credential); err == nil {
		t.Fatal("Verify() accepted a tampered body")
	}
}

func TestEd25519AdapterVerifiesSignatureAndRejectsWrongKey(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	request := SignedRequest{
		Timestamp: 1_800_000_000,
		EventID:   "evt-card-1",
		Body:      []byte(`{"id":"evt-card-1","type":"card.authorization.created"}`),
	}
	request.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, CanonicalPayload(request)))

	adapter := Ed25519Adapter{}
	credential := Credential{KeyID: "card-v1", Ed25519PublicKey: privateKey.Public().(ed25519.PublicKey)}
	if err := adapter.Verify(request, credential); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	wrongSeed := make([]byte, ed25519.SeedSize)
	wrongKey := ed25519.NewKeyFromSeed(wrongSeed).Public().(ed25519.PublicKey)
	credential.Ed25519PublicKey = wrongKey
	if err := adapter.Verify(request, credential); err == nil {
		t.Fatal("Verify() accepted a signature from another key")
	}
}
