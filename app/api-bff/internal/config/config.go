package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type GenericFieldConfig struct {
	ProtoName string `yaml:"ProtoName"`
	JSONName  string `yaml:"JSONName"`
	Kind      string `yaml:"Kind"`
	Repeated  bool   `yaml:"Repeated"`
}

type GenericSchemaConfig struct {
	Name             string `yaml:"Name"`
	Service          string `yaml:"Service"`
	Schema           string `yaml:"Schema"`
	Version          string `yaml:"Version"`
	DescriptorPath   string `yaml:"DescriptorPath"`
	MetadataPath     string `yaml:"MetadataPath"`
	DescriptorSHA256 string `yaml:"DescriptorSHA256"`
	Target           string `yaml:"Target"`
	Provider         string `yaml:"Provider"`
	NacosAddress     string `yaml:"NacosAddress"`
	EtcdEndpoints    string `yaml:"EtcdEndpoints"`
}

type GenericRouteConfig struct {
	Name         string               `yaml:"Name"`
	SchemaRef    string               `yaml:"SchemaRef"`
	Method       string               `yaml:"Method"`
	Timeout      string               `yaml:"Timeout"`
	InputFields  []GenericFieldConfig `yaml:"InputFields"`
	OutputFields []GenericFieldConfig `yaml:"OutputFields"`
}

type UserRPCConfig struct {
	Target        string
	Provider      string
	ServiceName   string
	NacosAddress  string
	EtcdEndpoints string
}

type GenericRPCConfig struct {
	ReloadInterval string
	Schemas        []GenericSchemaConfig
	Routes         []GenericRouteConfig
}

type Config struct {
	Name       string
	Host       string
	Port       int
	Timeout    time.Duration
	Mode       string
	UserRpc    UserRPCConfig
	GenericRPC GenericRPCConfig
}

type fileConfig struct {
	Name      string `yaml:"Name"`
	Host      string `yaml:"Host"`
	Port      int    `yaml:"Port"`
	TimeoutMS int64  `yaml:"Timeout"`
	Mode      string `yaml:"Mode"`
	UserRPC   struct {
		Target        string `yaml:"Target"`
		Provider      string `yaml:"Provider"`
		ServiceName   string `yaml:"ServiceName"`
		NacosAddress  string `yaml:"NacosAddress"`
		EtcdEndpoints string `yaml:"EtcdEndpoints"`
	} `yaml:"UserRpc"`
	GenericRPC struct {
		ReloadInterval string                `yaml:"ReloadInterval"`
		Schemas        []GenericSchemaConfig `yaml:"Schemas"`
		Routes         []GenericRouteConfig  `yaml:"Routes"`
	} `yaml:"GenericRPC"`
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
	if raw.Name == "" || raw.Host == "" || raw.Port <= 0 {
		return Config{}, fmt.Errorf("name, host and positive port are required")
	}
	timeout := 3 * time.Second
	if raw.TimeoutMS > 0 {
		timeout = time.Duration(raw.TimeoutMS) * time.Millisecond
	}
	return Config{
		Name: raw.Name, Host: raw.Host, Port: raw.Port, Timeout: timeout, Mode: raw.Mode,
		UserRpc: UserRPCConfig{
			Target: raw.UserRPC.Target, Provider: raw.UserRPC.Provider, ServiceName: raw.UserRPC.ServiceName,
			NacosAddress: raw.UserRPC.NacosAddress, EtcdEndpoints: raw.UserRPC.EtcdEndpoints,
		},
		GenericRPC: GenericRPCConfig{
			ReloadInterval: raw.GenericRPC.ReloadInterval,
			Schemas:        append([]GenericSchemaConfig(nil), raw.GenericRPC.Schemas...),
			Routes:         append([]GenericRouteConfig(nil), raw.GenericRPC.Routes...),
		},
	}, nil
}

func (c Config) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
