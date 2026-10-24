package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"
)

func TestCardCredentialsAcceptsBothKeysDuringRotation(t *testing.T) {
	_, v1Private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, v2Private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	v1 := base64.StdEncoding.EncodeToString(v1Private.Public().(ed25519.PublicKey))
	v2 := base64.StdEncoding.EncodeToString(v2Private.Public().(ed25519.PublicKey))

	credentials, err := cardCredentials(v1, v2, "card-v1,card-v2")
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 2 || credentials["card-v1"].KeyID != "card-v1" || credentials["card-v2"].KeyID != "card-v2" {
		t.Fatalf("credentials = %#v", credentials)
	}
}

func TestCardCredentialsCanRetireV1(t *testing.T) {
	_, v1Private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, v2Private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	v1 := base64.StdEncoding.EncodeToString(v1Private.Public().(ed25519.PublicKey))
	v2 := base64.StdEncoding.EncodeToString(v2Private.Public().(ed25519.PublicKey))

	credentials, err := cardCredentials(v1, v2, "card-v2")
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 || credentials["card-v2"].KeyID != "card-v2" {
		t.Fatalf("credentials = %#v", credentials)
	}
	if _, exists := credentials["card-v1"]; exists {
		t.Fatal("card-v1 remains active")
	}
}
