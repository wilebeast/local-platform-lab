package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestKratosConfigLoadsExistingUserServiceContract(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "etc", "user-service.yaml"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Name != "user-service" || cfg.GRPC.Address != "0.0.0.0:9001" || cfg.GRPC.Timeout != 3*time.Second {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if !cfg.IsSensitiveMethod("/user.v1.UserService/Login") || cfg.IsSensitiveMethod("/user.v1.UserService/GetProfile") {
		t.Fatalf("unexpected sensitive-method policy: %#v", cfg.SensitiveMethods)
	}
}
