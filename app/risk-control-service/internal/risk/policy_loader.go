package risk

import "context"

type StoredPolicyDefinition struct {
	Policy  ExecutablePolicyDefinition
	Factors []FactorDefinition
}

type PolicyDefinitionRepository interface {
	LoadPublishedPolicyDefinition(context.Context, string, int) (StoredPolicyDefinition, error)
}

type PolicySnapshotLoader struct {
	repository          PolicyDefinitionRepository
	rpcAdapters         *RPCFactorAdapterRegistry
	additionalProviders []FactorProvider
}

func NewPolicySnapshotLoader(
	repository PolicyDefinitionRepository,
	rpcAdapters *RPCFactorAdapterRegistry,
) *PolicySnapshotLoader {
	return &PolicySnapshotLoader{repository: repository, rpcAdapters: rpcAdapters}
}

func NewPolicySnapshotLoaderWithProviders(
	repository PolicyDefinitionRepository,
	rpcAdapters *RPCFactorAdapterRegistry,
	additionalProviders []FactorProvider,
) *PolicySnapshotLoader {
	return &PolicySnapshotLoader{
		repository: repository, rpcAdapters: rpcAdapters,
		additionalProviders: append([]FactorProvider(nil), additionalProviders...),
	}
}

func (l *PolicySnapshotLoader) LoadPublished(
	ctx context.Context,
	policyKey string,
	version int,
) (*CompiledPolicySnapshot, error) {
	definition, err := l.repository.LoadPublishedPolicyDefinition(ctx, policyKey, version)
	if err != nil {
		return nil, err
	}
	return CompilePolicySnapshotWithProviders(
		definition.Policy, definition.Factors, l.rpcAdapters, l.additionalProviders,
	)
}
