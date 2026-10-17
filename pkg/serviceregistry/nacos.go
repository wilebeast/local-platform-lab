package serviceregistry

import (
	"fmt"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

func newNacosClient(address string) (naming_client.INamingClient, error) {
	host, port, err := splitAddress(address)
	if err != nil {
		return nil, err
	}
	return clients.NewNamingClient(vo.NacosClientParam{
		ClientConfig: &constant.ClientConfig{
			NamespaceId:         "public",
			TimeoutMs:           5000,
			NotLoadCacheAtStart: true,
			CacheDir:            "/tmp/nacos/cache",
			LogDir:              "/tmp/nacos/log",
			LogLevel:            "warn",
		},
		ServerConfigs: []constant.ServerConfig{{IpAddr: host, Port: port}},
	})
}

type nacosDiscoverer struct {
	client naming_client.INamingClient
}

func newNacosDiscoverer(address string) (*nacosDiscoverer, error) {
	client, err := newNacosClient(address)
	if err != nil {
		return nil, err
	}
	return &nacosDiscoverer{client: client}, nil
}

func (d *nacosDiscoverer) list(serviceName string) ([]string, error) {
	instances, err := d.client.SelectInstances(vo.SelectInstancesParam{
		ServiceName: serviceName,
		GroupName:   "DEFAULT_GROUP",
		Clusters:    []string{"DEFAULT"},
		HealthyOnly: true,
	})
	if err != nil {
		return nil, err
	}
	addresses := make([]string, 0, len(instances))
	for _, instance := range instances {
		if instance.Enable && instance.Healthy {
			addresses = append(addresses, fmt.Sprintf("%s:%d", instance.Ip, instance.Port))
		}
	}
	return addresses, nil
}

func (d *nacosDiscoverer) close() {
	d.client.CloseClient()
}
