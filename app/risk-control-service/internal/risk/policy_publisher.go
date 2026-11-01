package risk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type PolicyPublication struct {
	PolicyKey          string
	PolicyVersion      int
	ContentFingerprint string
	Snapshot           *CompiledPolicySnapshot
}

type PolicyPublisher struct {
	store       *PostgresStore
	rpcAdapters *RPCFactorAdapterRegistry
}

func NewPolicyPublisher(
	store *PostgresStore,
	rpcAdapters *RPCFactorAdapterRegistry,
) *PolicyPublisher {
	return &PolicyPublisher{store: store, rpcAdapters: rpcAdapters}
}

func (p *PolicyPublisher) Publish(
	ctx context.Context,
	policyKey string,
	version int,
) (PolicyPublication, error) {
	if policyKey == "" || version <= 0 {
		return PolicyPublication{}, fmt.Errorf("policy_key and positive version are required")
	}
	tx, err := p.store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PolicyPublication{}, err
	}
	defer tx.Rollback(ctx)

	var status string
	err = tx.QueryRow(ctx, `
		SELECT status
		FROM risk_decision_policy_versions
		WHERE policy_key=$1 AND policy_version=$2
		FOR UPDATE`, policyKey, version).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return PolicyPublication{}, NotFoundError{"policy version not found"}
	}
	if err != nil {
		return PolicyPublication{}, err
	}
	if status != "draft" {
		return PolicyPublication{}, ConflictError{"only a draft policy version can be published"}
	}

	definition, err := loadPolicyDefinition(ctx, tx, policyKey, version, "draft")
	if err != nil {
		return PolicyPublication{}, err
	}
	snapshot, err := CompilePolicySnapshot(definition.Policy, definition.Factors, p.rpcAdapters)
	if err != nil {
		return PolicyPublication{}, fmt.Errorf("compile policy before publication: %w", err)
	}
	fingerprint, err := policyDefinitionFingerprint(definition)
	if err != nil {
		return PolicyPublication{}, err
	}

	commandTag, err := tx.Exec(ctx, `
		UPDATE risk_decision_policy_versions
		SET status='published', content_fingerprint=$3, published_at=NOW()
		WHERE policy_key=$1 AND policy_version=$2 AND status='draft'`,
		policyKey, version, fingerprint)
	if err != nil {
		return PolicyPublication{}, err
	}
	if commandTag.RowsAffected() != 1 {
		return PolicyPublication{}, ConflictError{"policy version changed during publication"}
	}
	if err := tx.Commit(ctx); err != nil {
		return PolicyPublication{}, err
	}
	return PolicyPublication{
		PolicyKey:          policyKey,
		PolicyVersion:      version,
		ContentFingerprint: fingerprint,
		Snapshot:           snapshot,
	}, nil
}

func policyDefinitionFingerprint(definition StoredPolicyDefinition) (string, error) {
	encoded, err := json.Marshal(definition)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
