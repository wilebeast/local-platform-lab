package apikey

import "testing"

func TestIPAllowedAcceptsIPv6RemoteAddressWithPort(t *testing.T) {
	if !ipAllowed("[2001:db8::1]:443", []string{"2001:db8::/32"}) {
		t.Fatal("expected IPv6 remote address to match whitelist prefix")
	}
}
