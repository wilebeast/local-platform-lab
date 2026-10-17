package serviceregistry

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	ProviderNacos = "nacos"
	ProviderEtcd  = "etcd"
	ProviderDNS   = "dns"
)

type Config struct {
	Provider      string
	ServiceName   string
	NacosAddress  string
	EtcdEndpoints string
	DNSFallback   string
	PollInterval  time.Duration
}

func (c Config) normalized() Config {
	c.Provider = strings.ToLower(strings.TrimSpace(c.Provider))
	if c.Provider == "" {
		c.Provider = ProviderNacos
	}
	if c.NacosAddress == "" {
		c.NacosAddress = "nacos.nacos.svc.cluster.local:8848"
	}
	if c.EtcdEndpoints == "" {
		c.EtcdEndpoints = "etcd-client.etcd.svc.cluster.local:2379"
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 2 * time.Second
	}
	return c
}

func splitAddress(address string) (string, uint64, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("split address %q: %w", address, err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return "", 0, fmt.Errorf("parse port %q: %w", portText, err)
	}
	return host, port, nil
}

func splitEndpoints(endpoints string) []string {
	parts := strings.Split(endpoints, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
