package config

import "github.com/zeromicro/go-zero/rest"

type GenericFieldConfig struct {
	ProtoName string
	JSONName  string
	Kind      string
	Repeated  bool
}

type GenericSchemaConfig struct {
	Name             string
	Service          string
	Schema           string
	Version          string
	DescriptorPath   string
	MetadataPath     string
	DescriptorSHA256 string
	Target           string
	Provider         string
	NacosAddress     string
	EtcdEndpoints    string
}

type GenericRouteConfig struct {
	Name         string
	SchemaRef    string
	Method       string
	Timeout      string
	InputFields  []GenericFieldConfig
	OutputFields []GenericFieldConfig
}

type Config struct {
	rest.RestConf
	UserRpc struct {
		Target        string
		Provider      string
		ServiceName   string
		NacosAddress  string
		EtcdEndpoints string
	}
	GenericRPC struct {
		ReloadInterval string
		Schemas        []GenericSchemaConfig
		Routes         []GenericRouteConfig
	}
}
