package riskwarehouse

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/risk"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

const outflowRatioResolverKey = "subject_fund_activity_24h/outflow_ratio"

const (
	inflowMinorResolverKey  = "subject_fund_activity_24h/inflow_minor"
	outflowMinorResolverKey = "subject_fund_activity_24h/outflow_minor"
)

type FeatureSnapshotQuery struct {
	FeatureSetVersion int32
	SubjectType       string
	SubjectID         string
	Currency          string
	AsOf              time.Time
}

type FundActivitySnapshotReader interface {
	ReadFundActivitySnapshot(context.Context, FeatureSnapshotQuery) (warehouse.SubjectFundActivitySnapshot, bool, error)
}

type FeatureAdapter interface {
	Key() string
	ValueType() risk.FactorValueType
	FeatureSetVersion() int32
	MaxAge() time.Duration
	Extract(warehouse.SubjectFundActivitySnapshot) (any, bool)
}

type FeatureAdapterRegistry struct {
	mu       sync.RWMutex
	adapters map[string]FeatureAdapter
}

func NewFeatureAdapterRegistry() *FeatureAdapterRegistry {
	return &FeatureAdapterRegistry{adapters: make(map[string]FeatureAdapter)}
}

func (r *FeatureAdapterRegistry) Register(adapter FeatureAdapter) error {
	if adapter == nil || adapter.Key() == "" {
		return fmt.Errorf("warehouse feature adapter and key are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[adapter.Key()]; exists {
		return fmt.Errorf("warehouse feature adapter %q is already registered", adapter.Key())
	}
	r.adapters[adapter.Key()] = adapter
	return nil
}

func (r *FeatureAdapterRegistry) get(key string) (FeatureAdapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, found := r.adapters[key]
	return adapter, found
}

type fundActivityOutflowRatioAdapter struct {
	version int32
	maxAge  time.Duration
}

type fundActivityAmountAdapter struct {
	key     string
	version int32
	maxAge  time.Duration
	extract func(warehouse.SubjectFundActivitySnapshot) int64
}

func NewFundActivityInflowMinorAdapter(version int32, maxAge time.Duration) FeatureAdapter {
	return &fundActivityAmountAdapter{
		key: inflowMinorResolverKey, version: version, maxAge: maxAge,
		extract: func(snapshot warehouse.SubjectFundActivitySnapshot) int64 { return snapshot.InflowMinor },
	}
}

func NewFundActivityOutflowMinorAdapter(version int32, maxAge time.Duration) FeatureAdapter {
	return &fundActivityAmountAdapter{
		key: outflowMinorResolverKey, version: version, maxAge: maxAge,
		extract: func(snapshot warehouse.SubjectFundActivitySnapshot) int64 { return snapshot.OutflowMinor },
	}
}

func (a *fundActivityAmountAdapter) Key() string { return a.key }
func (*fundActivityAmountAdapter) ValueType() risk.FactorValueType {
	return risk.FactorValueTypeInt
}
func (a *fundActivityAmountAdapter) FeatureSetVersion() int32 { return a.version }
func (a *fundActivityAmountAdapter) MaxAge() time.Duration    { return a.maxAge }
func (a *fundActivityAmountAdapter) Extract(snapshot warehouse.SubjectFundActivitySnapshot) (any, bool) {
	return a.extract(snapshot), true
}

func NewFundActivityOutflowRatioAdapter(version int32, maxAge time.Duration) FeatureAdapter {
	return &fundActivityOutflowRatioAdapter{version: version, maxAge: maxAge}
}

func (*fundActivityOutflowRatioAdapter) Key() string { return outflowRatioResolverKey }
func (*fundActivityOutflowRatioAdapter) ValueType() risk.FactorValueType {
	return risk.FactorValueTypeDouble
}
func (a *fundActivityOutflowRatioAdapter) FeatureSetVersion() int32 { return a.version }
func (a *fundActivityOutflowRatioAdapter) MaxAge() time.Duration    { return a.maxAge }
func (*fundActivityOutflowRatioAdapter) Extract(snapshot warehouse.SubjectFundActivitySnapshot) (any, bool) {
	if !snapshot.OutflowRatioDefined {
		return nil, false
	}
	return snapshot.OutflowRatio, true
}

type WarehouseFeatureFactorProvider struct {
	reader   FundActivitySnapshotReader
	adapters *FeatureAdapterRegistry
}

func NewWarehouseFeatureFactorProvider(
	reader FundActivitySnapshotReader,
	adapters *FeatureAdapterRegistry,
) *WarehouseFeatureFactorProvider {
	return &WarehouseFeatureFactorProvider{reader: reader, adapters: adapters}
}

func (*WarehouseFeatureFactorProvider) Type() risk.FactorType {
	return risk.FactorTypeWarehouseFeature
}

func (p *WarehouseFeatureFactorProvider) NewFactor(definition risk.FactorDefinition) (risk.Factor, error) {
	if definition.Key == "" || definition.Type != risk.FactorTypeWarehouseFeature {
		return nil, fmt.Errorf("valid warehouse feature factor definition is required")
	}
	if p.reader == nil || p.adapters == nil {
		return nil, fmt.Errorf("warehouse feature reader and adapter registry are required")
	}
	adapter, found := p.adapters.get(definition.ResolverKey)
	if !found {
		return nil, fmt.Errorf("warehouse feature adapter %q is not registered", definition.ResolverKey)
	}
	if definition.ValueType != adapter.ValueType() {
		return nil, fmt.Errorf("warehouse feature %q expects %s, got %s", definition.Key, adapter.ValueType(), definition.ValueType)
	}
	if definition.ParamKey != "" || definition.Expression != "" || definition.Timeout != 0 {
		return nil, fmt.Errorf("warehouse feature factor %q must define only resolver_key", definition.Key)
	}
	return &warehouseFeatureFactor{key: definition.Key, reader: p.reader, adapter: adapter}, nil
}

type warehouseFeatureFactor struct {
	key     string
	reader  FundActivitySnapshotReader
	adapter FeatureAdapter
}

func (f *warehouseFeatureFactor) Key() string                 { return f.key }
func (*warehouseFeatureFactor) DependentFactorKeys() []string { return nil }

func (f *warehouseFeatureFactor) Fetch(ctx context.Context, runtime risk.FactorRuntime) risk.FactorResult {
	query, result := featureSnapshotQuery(f.key, f.adapter.FeatureSetVersion(), runtime)
	if result != nil {
		return *result
	}
	snapshot, found, err := f.reader.ReadFundActivitySnapshot(ctx, query)
	if err != nil {
		return failure(f.key, risk.FactorStatusError, "warehouse_read_failed", err.Error())
	}
	if !found {
		return failure(f.key, risk.FactorStatusMissing, "feature_snapshot_missing", "feature snapshot was not found")
	}
	snapshotAsOf := time.UnixMilli(snapshot.AsOfMillis).UTC()
	if snapshotAsOf.After(query.AsOf) {
		return failure(f.key, risk.FactorStatusError, "future_feature", "feature snapshot is newer than evaluation as_of")
	}
	if maxAge := f.adapter.MaxAge(); maxAge >= 0 && query.AsOf.Sub(snapshotAsOf) > maxAge {
		return failure(f.key, risk.FactorStatusError, "stale_feature", "feature snapshot exceeds maximum age")
	}
	value, defined := f.adapter.Extract(snapshot)
	if !defined {
		return failure(f.key, risk.FactorStatusMissing, "feature_undefined", "feature value is undefined")
	}
	return risk.FactorResult{Key: f.key, Status: risk.FactorStatusSuccess, Value: value}
}

func featureSnapshotQuery(key string, version int32, runtime risk.FactorRuntime) (FeatureSnapshotQuery, *risk.FactorResult) {
	stringParam := func(param string) (string, bool) {
		value, found := runtime.ParamValue(param)
		converted, ok := value.(string)
		return converted, found && ok && converted != ""
	}
	subjectType, ok := stringParam("subject_type")
	if !ok {
		result := failure(key, risk.FactorStatusMissing, "missing_subject_type", "subject_type is required")
		return FeatureSnapshotQuery{}, &result
	}
	subjectID, ok := stringParam("subject_id")
	if !ok {
		result := failure(key, risk.FactorStatusMissing, "missing_subject_id", "subject_id is required")
		return FeatureSnapshotQuery{}, &result
	}
	currency, ok := stringParam("currency")
	if !ok {
		result := failure(key, risk.FactorStatusMissing, "missing_currency", "currency is required")
		return FeatureSnapshotQuery{}, &result
	}
	asOfValue, found := runtime.ParamValue("as_of")
	asOf, ok := asOfValue.(time.Time)
	if !found || !ok || asOf.IsZero() {
		result := failure(key, risk.FactorStatusMissing, "missing_as_of", "as_of time is required")
		return FeatureSnapshotQuery{}, &result
	}
	return FeatureSnapshotQuery{
		FeatureSetVersion: version, SubjectType: subjectType, SubjectID: subjectID,
		Currency: currency, AsOf: asOf.UTC(),
	}, nil
}

func failure(key string, status risk.FactorStatus, code, message string) risk.FactorResult {
	return risk.FactorResult{Key: key, Status: status, ErrorCode: code, ErrorMessage: message}
}
