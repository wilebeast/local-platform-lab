package va

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

type Rail struct{ VirtualAccountID, Type, RoutingCode, MaskedAccount string }
type ProvisionResult struct {
	OpeningStatus, AccountID, ProviderResourceID string
	Rails                                        []Rail
}

func Provision(openingID, ownerID, currency string) ProvisionResult {
	accountID := "va-" + short(openingID+":"+ownerID+":"+currency)
	return ProvisionResult{"active", accountID, "provider-" + accountID, []Rail{
		{accountID, "local_transfer", "110000", "******" + short(accountID)[:4]},
		{accountID, "wire", "DEMOUS33", "******" + short(accountID)[4:8]},
	}}
}

func short(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:])[:24] }
func Sign(secret, timestamp, eventID string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(m, "%s.%s.", timestamp, eventID)
	_, _ = m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}
func Verify(secret, timestamp, eventID string, body []byte, signature string) bool {
	want, err := hex.DecodeString(Sign(secret, timestamp, eventID, body))
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(signature)
	return err == nil && hmac.Equal(want, got)
}
