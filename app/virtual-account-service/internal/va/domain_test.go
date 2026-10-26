package va

import "testing"

func TestOpeningCreatesOneAccountWithMultipleRails(t *testing.T) {
	a := Provision("opening-1", "org-1", "USD")
	if a.OpeningStatus != "active" || len(a.Rails) != 2 {
		t.Fatalf("unexpected provision result: %#v", a)
	}
	if a.Rails[0].VirtualAccountID != a.Rails[1].VirtualAccountID {
		t.Fatal("rails must belong to one virtual account")
	}
}

func TestVerifyProviderSignatureBindsHeadersAndBody(t *testing.T) {
	body := []byte(`{"id":"evt-1","amount_minor":1000}`)
	sig := Sign("secret", "1700000000", "evt-1", body)
	if !Verify("secret", "1700000000", "evt-1", body, sig) {
		t.Fatal("valid signature rejected")
	}
	if Verify("secret", "1700000000", "evt-1", []byte(`{"id":"evt-1","amount_minor":9000}`), sig) {
		t.Fatal("tampered body accepted")
	}
}
