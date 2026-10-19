package genericrpc

import (
	"context"
	"testing"
)

type recordingCaller struct {
	service string
	method  string
}

func (c *recordingCaller) Call(_ context.Context, method string, request map[string]any) (map[string]any, error) {
	c.method = method
	return map[string]any{"service": c.service, "value": request["value"]}, nil
}

func TestRegistryDispatchesRoutesAcrossSchemas(t *testing.T) {
	users := &recordingCaller{service: "users"}
	accounts := &recordingCaller{service: "accounts"}
	registry, err := NewRegistry(map[string]Caller{"user-v1": users, "account-v1": accounts}, []RouteDefinition{
		{Name: "get-profile", SchemaRef: "user-v1", Method: "/user.v1.UserService/GetProfile"},
		{Name: "get-balance", SchemaRef: "account-v1", Method: "/account.v1.AccountService/GetBalance"},
	})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	profile, err := registry.CallRoute(context.Background(), "get-profile", map[string]any{"value": "user-123"})
	if err != nil {
		t.Fatalf("call profile: %v", err)
	}
	balance, err := registry.CallRoute(context.Background(), "get-balance", map[string]any{"value": "account-456"})
	if err != nil {
		t.Fatalf("call balance: %v", err)
	}
	if profile["service"] != "users" || balance["service"] != "accounts" {
		t.Fatalf("routes used wrong schemas: profile=%v balance=%v", profile, balance)
	}
	if users.method != "/user.v1.UserService/GetProfile" || accounts.method != "/account.v1.AccountService/GetBalance" {
		t.Fatalf("routes used wrong methods: users=%q accounts=%q", users.method, accounts.method)
	}
}

func TestRegistryRejectsUnknownSchemaAndRoute(t *testing.T) {
	_, err := NewRegistry(map[string]Caller{"user-v1": &recordingCaller{}}, []RouteDefinition{{
		Name: "missing", SchemaRef: "missing-v1", Method: "/missing.Service/Get",
	}})
	if err == nil {
		t.Fatal("expected unknown schema error")
	}

	registry, err := NewRegistry(map[string]Caller{"user-v1": &recordingCaller{}}, []RouteDefinition{{
		Name: "get-profile", SchemaRef: "user-v1", Method: "/user.v1.UserService/GetProfile",
	}})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if _, err := registry.CallRoute(context.Background(), "missing", nil); err == nil {
		t.Fatal("expected unknown route error")
	}
}

func TestRegistryRejectsDuplicateRoute(t *testing.T) {
	caller := &recordingCaller{}
	_, err := NewRegistry(map[string]Caller{"user-v1": caller}, []RouteDefinition{
		{Name: "same", SchemaRef: "user-v1", Method: "/one.Service/Get"},
		{Name: "same", SchemaRef: "user-v1", Method: "/two.Service/Get"},
	})
	if err == nil {
		t.Fatal("expected duplicate route error")
	}
}
