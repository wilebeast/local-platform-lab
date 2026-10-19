package config

import (
	"path/filepath"
	"testing"

	"github.com/zeromicro/go-zero/core/conf"
)

func TestLocalConfigLoads(t *testing.T) {
	path := filepath.Join("..", "..", "etc", "api-bff.yaml")

	var cfg Config
	if err := conf.Load(path, &cfg); err != nil {
		t.Fatalf("load local config: %v", err)
	}
	if cfg.Name != "api-bff" || cfg.Port != 8888 || cfg.UserRpc.Target == "" {
		t.Fatalf("unexpected config: name=%q port=%d userTarget=%q", cfg.Name, cfg.Port, cfg.UserRpc.Target)
	}
	if cfg.GenericRPC.ReloadInterval != "10s" || len(cfg.GenericRPC.Schemas) != 2 || len(cfg.GenericRPC.Routes) != 3 {
		t.Fatalf("unexpected generic RPC config: %#v", cfg.GenericRPC)
	}
	schema := cfg.GenericRPC.Schemas[0]
	if schema.Name != "user-v1" || schema.Service != "user-service" || schema.DescriptorPath == "" || schema.MetadataPath == "" || schema.Version != "1.0.0" {
		t.Fatalf("unexpected generic RPC schema: %#v", schema)
	}
	route := cfg.GenericRPC.Routes[0]
	if route.Name != "get-profile" || route.SchemaRef != "user-v1" || route.Method != "/user.v1.UserService/GetProfile" || len(route.InputFields) != 1 || len(route.OutputFields) != 2 {
		t.Fatalf("unexpected generic RPC route: %#v", route)
	}
	account := cfg.GenericRPC.Schemas[1]
	if account.Name != "account-v1" || account.Service != "account-service" || account.Schema != "account.v1.AccountService" {
		t.Fatalf("unexpected account schema: %#v", account)
	}
	if cfg.GenericRPC.Routes[1].SchemaRef != "account-v1" || cfg.GenericRPC.Routes[2].SchemaRef != "account-v1" {
		t.Fatalf("account routes are not bound to account-v1: %#v", cfg.GenericRPC.Routes)
	}
}
