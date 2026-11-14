package main

import (
	"testing"

	userconfig "github.com/wilebeast/local-platform-lab/app/user-service/internal/config"
)

func TestSensitiveRPCPayloadsAreNotLogged(t *testing.T) {
	config, err := userconfig.Load("../../etc/user-service.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{
		"/user.v1.UserService/Login",
		"/user.v1.UserService/PrepareLogin",
		"/user.v1.UserService/VerifyLogin2FA",
		"/user.v1.UserService/ConfirmTOTP",
		"/user.v1.UserService/UnbindTOTP",
		"/user.v1.UserService/RegenerateRecoveryCodes",
		"/user.v1.UserService/FinishPasskeyRegistration",
		"/user.v1.UserService/FinishPasskeyLogin",
		"/user.v1.UserService/Refresh",
		"/user.v1.UserService/Logout",
	} {
		if !config.IsSensitiveMethod(method) {
			t.Errorf("sensitive RPC payload is still logged: %s", method)
		}
	}
}

func TestKubernetesDNSModeCanSkipExternalRegistryRegistration(t *testing.T) {
	if registrationEnabled("disabled") {
		t.Fatal("disabled registration mode must not contact Nacos or Etcd")
	}
	if !registrationEnabled("") || !registrationEnabled("both") {
		t.Fatal("existing dual-registration behavior must remain the default")
	}
}

func TestBootstrapIdentityRequiresExplicitOptIn(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "", want: false},
		{value: "false", want: false},
		{value: "disabled", want: false},
		{value: "true", want: true},
		{value: "1", want: true},
		{value: " ENABLED ", want: true},
	}
	for _, test := range tests {
		if got := bootstrapIdentityEnabled(test.value); got != test.want {
			t.Fatalf("bootstrapIdentityEnabled(%q)=%v, want %v", test.value, got, test.want)
		}
	}
}
