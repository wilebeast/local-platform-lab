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
}
