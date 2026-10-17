package serviceregistry

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type Registration struct {
	cleanups []func()
}

func RegisterBoth(ctx context.Context, cfg Config, address string) (*Registration, error) {
	cfg = cfg.normalized()
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split advertised address: %w", err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("parse advertised port: %w", err)
	}

	registration := &Registration{}
	nacosClient, err := newNacosClient(cfg.NacosAddress)
	if err != nil {
		return nil, fmt.Errorf("create Nacos client: %w", err)
	}
	nacosParam := vo.RegisterInstanceParam{
		Ip: host, Port: port, ServiceName: cfg.ServiceName,
		GroupName: "DEFAULT_GROUP", ClusterName: "DEFAULT",
		Weight: 1, Enable: true, Healthy: true, Ephemeral: true,
	}
	ok, err := nacosClient.RegisterInstance(nacosParam)
	if err != nil || !ok {
		nacosClient.CloseClient()
		return nil, fmt.Errorf("register Nacos instance: ok=%v err=%w", ok, err)
	}
	registration.cleanups = append(registration.cleanups, func() {
		_, _ = nacosClient.DeregisterInstance(vo.DeregisterInstanceParam{
			Ip: host, Port: port, ServiceName: cfg.ServiceName,
			GroupName: "DEFAULT_GROUP", Cluster: "DEFAULT", Ephemeral: true,
		})
		nacosClient.CloseClient()
	})

	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints: splitEndpoints(cfg.EtcdEndpoints), DialTimeout: 5 * time.Second,
	})
	if err != nil {
		registration.Close()
		return nil, fmt.Errorf("create Etcd client: %w", err)
	}
	lease, err := etcdClient.Grant(ctx, 10)
	if err != nil {
		_ = etcdClient.Close()
		registration.Close()
		return nil, fmt.Errorf("create Etcd lease: %w", err)
	}
	key := etcdPrefix + cfg.ServiceName + "/" + address
	if _, err = etcdClient.Put(ctx, key, address, clientv3.WithLease(lease.ID)); err != nil {
		_ = etcdClient.Close()
		registration.Close()
		return nil, fmt.Errorf("register Etcd instance: %w", err)
	}
	keepAlive, err := etcdClient.KeepAlive(ctx, lease.ID)
	if err != nil {
		_ = etcdClient.Close()
		registration.Close()
		return nil, fmt.Errorf("keep Etcd lease alive: %w", err)
	}
	go func() {
		for range keepAlive {
		}
	}()
	registration.cleanups = append(registration.cleanups, func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = etcdClient.Revoke(cleanupCtx, lease.ID)
		_ = etcdClient.Close()
	})
	return registration, nil
}

func (r *Registration) Close() {
	for index := len(r.cleanups) - 1; index >= 0; index-- {
		r.cleanups[index]()
	}
	r.cleanups = nil
}
