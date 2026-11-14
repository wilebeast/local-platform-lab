package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Name             string
	GRPC             GRPCConfig
	SensitiveMethods []string
	sensitiveMethods map[string]struct{}
}

type GRPCConfig struct {
	Address string
	Timeout time.Duration
}

type fileConfig struct {
	Name string `yaml:"Name"`
	GRPC struct {
		Address string `yaml:"Address"`
		Timeout string `yaml:"Timeout"`
	} `yaml:"GRPC"`
	SensitiveMethods []string `yaml:"SensitiveMethods"`
}

func Load(path string) (Config, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var raw fileConfig
	if err := yaml.Unmarshal(contents, &raw); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if raw.Name == "" || raw.GRPC.Address == "" {
		return Config{}, fmt.Errorf("name and GRPC address are required")
	}
	timeout := 3 * time.Second
	if raw.GRPC.Timeout != "" {
		timeout, err = time.ParseDuration(raw.GRPC.Timeout)
		if err != nil {
			return Config{}, fmt.Errorf("parse GRPC timeout: %w", err)
		}
	}
	cfg := Config{
		Name:             raw.Name,
		GRPC:             GRPCConfig{Address: raw.GRPC.Address, Timeout: timeout},
		SensitiveMethods: append([]string(nil), raw.SensitiveMethods...),
		sensitiveMethods: make(map[string]struct{}, len(raw.SensitiveMethods)),
	}
	for _, method := range raw.SensitiveMethods {
		cfg.sensitiveMethods[method] = struct{}{}
	}
	return cfg, nil
}

func (c Config) IsSensitiveMethod(method string) bool {
	_, ok := c.sensitiveMethods[method]
	return ok
}
