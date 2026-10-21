package main

import (
	"testing"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/zrpc"
)

func TestSensitiveRPCPayloadsAreNotLogged(t *testing.T) {
	var config zrpc.RpcServerConf
	conf.MustLoad("../../etc/user-service.yaml", &config)
	ignored := make(map[string]bool, len(config.Middlewares.StatConf.IgnoreContentMethods))
	for _, method := range config.Middlewares.StatConf.IgnoreContentMethods {
		ignored[method] = true
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
		if !ignored[method] {
			t.Errorf("sensitive RPC payload is still logged: %s", method)
		}
	}
}
