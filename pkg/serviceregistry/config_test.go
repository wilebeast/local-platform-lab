package serviceregistry

import (
	"reflect"
	"testing"
)

func TestBuilderSchemeIsUniquePerService(t *testing.T) {
	users := resolverScheme(ProviderNacos, "user-service")
	accounts := resolverScheme(ProviderNacos, "account-service")
	if users == accounts {
		t.Fatal("different services must not share a resolver scheme")
	}
	if users != "local-nacos-user-service" {
		t.Fatalf("unexpected resolver scheme %q", users)
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := (Config{}).normalized()
	if cfg.Provider != ProviderNacos {
		t.Fatalf("provider = %q, want %q", cfg.Provider, ProviderNacos)
	}
	if cfg.NacosAddress != "nacos.nacos.svc.cluster.local:8848" {
		t.Fatalf("unexpected Nacos address %q", cfg.NacosAddress)
	}
}

func TestSplitEndpoints(t *testing.T) {
	got := splitEndpoints("etcd-1:2379, etcd-2:2379, ")
	want := []string{"etcd-1:2379", "etcd-2:2379"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitEndpoints() = %#v, want %#v", got, want)
	}
}
