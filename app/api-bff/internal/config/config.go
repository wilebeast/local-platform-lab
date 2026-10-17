package config

import "github.com/zeromicro/go-zero/rest"

type Config struct {
	rest.RestConf
	UserRpc struct {
		Target        string
		Provider      string
		ServiceName   string
		NacosAddress  string
		EtcdEndpoints string
	}
}
