package serviceregistry

import (
	"context"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

const etcdPrefix = "/local-platform/services/"

type etcdDiscoverer struct {
	client *clientv3.Client
}

func newEtcdDiscoverer(endpoints string) (*etcdDiscoverer, error) {
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   splitEndpoints(endpoints),
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &etcdDiscoverer{client: client}, nil
}

func (d *etcdDiscoverer) list(serviceName string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prefix := etcdPrefix + serviceName + "/"
	response, err := d.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	addresses := make([]string, 0, len(response.Kvs))
	for _, item := range response.Kvs {
		address := strings.TrimSpace(string(item.Value))
		if address != "" {
			addresses = append(addresses, address)
		}
	}
	return addresses, nil
}

func (d *etcdDiscoverer) close() {
	_ = d.client.Close()
}
