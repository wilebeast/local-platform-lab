package serviceregistry

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/resolver"
)

type discoverer interface {
	list(serviceName string) ([]string, error)
	close()
}

type Builder struct {
	cfg        Config
	discoverer discoverer
	scheme     string
	closeOnce  sync.Once
}

func NewBuilder(cfg Config) (*Builder, error) {
	cfg = cfg.normalized()
	var source discoverer
	var err error
	switch cfg.Provider {
	case ProviderNacos:
		source, err = newNacosDiscoverer(cfg.NacosAddress)
	case ProviderEtcd:
		source, err = newEtcdDiscoverer(cfg.EtcdEndpoints)
	default:
		return nil, fmt.Errorf("unsupported discovery provider %q", cfg.Provider)
	}
	if err != nil {
		return nil, err
	}
	return &Builder{cfg: cfg, discoverer: source, scheme: resolverScheme(cfg.Provider, cfg.ServiceName)}, nil
}

func resolverScheme(provider, serviceName string) string {
	schemeService := strings.NewReplacer("_", "-", ".", "-").Replace(strings.ToLower(serviceName))
	return "local-" + provider + "-" + schemeService
}

func (b *Builder) Scheme() string { return b.scheme }
func (b *Builder) Target() string { return b.scheme + ":///" + b.cfg.ServiceName }

func (b *Builder) Build(_ resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	r := &pollingResolver{builder: b, cc: cc, stop: make(chan struct{})}
	if err := r.update(); err != nil {
		cc.ReportError(err)
	}
	go r.watch()
	return r, nil
}

func (b *Builder) Close() { b.closeOnce.Do(func() { b.discoverer.close() }) }

type pollingResolver struct {
	builder *Builder
	cc      resolver.ClientConn
	stop    chan struct{}
	once    sync.Once
}

func (r *pollingResolver) update() error {
	addresses, err := r.builder.discoverer.list(r.builder.cfg.ServiceName)
	if err != nil {
		return err
	}
	if len(addresses) == 0 {
		return fmt.Errorf("no healthy %s instances found in %s", r.builder.cfg.ServiceName, r.builder.cfg.Provider)
	}
	resolved := make([]resolver.Address, 0, len(addresses))
	for _, address := range addresses {
		resolved = append(resolved, resolver.Address{Addr: address})
	}
	return r.cc.UpdateState(resolver.State{Addresses: resolved})
}

func (r *pollingResolver) watch() {
	ticker := time.NewTicker(r.builder.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := r.update(); err != nil {
				log.Printf("service discovery refresh failed: %v", err)
				r.cc.ReportError(err)
			}
		case <-r.stop:
			return
		}
	}
}

func (r *pollingResolver) ResolveNow(resolver.ResolveNowOptions) {
	if err := r.update(); err != nil {
		r.cc.ReportError(err)
	}
}

func (r *pollingResolver) Close() { r.once.Do(func() { close(r.stop) }) }
