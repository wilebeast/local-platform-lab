package featureplatform

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (r *PostgresRegistry) ResolveFeatures(
	ctx context.Context,
	refs []FeatureRef,
) ([]ServingFeatureDefinition, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("at least one feature reference is required")
	}
	var values strings.Builder
	args := make([]any, 0, len(refs)*4)
	aliases := make(map[string]string, len(refs))
	for index, ref := range refs {
		if ref.FeatureSetKey == "" || ref.FeatureSetVersion <= 0 || ref.FeatureKey == "" {
			return nil, fmt.Errorf("complete versioned feature references are required")
		}
		if index > 0 {
			values.WriteString(",")
		}
		base := index*4 + 1
		fmt.Fprintf(&values, "($%d::int,$%d::text,$%d::int,$%d::text)", base, base+1, base+2, base+3)
		args = append(args, index+1, ref.FeatureSetKey, ref.FeatureSetVersion, ref.FeatureKey)
		aliases[featureRefIdentity(ref)] = ref.Alias
	}
	query := fmt.Sprintf(`
		WITH requested(ordinal, feature_set_key, feature_set_version, feature_key) AS (
			VALUES %s
		)
		SELECT r.feature_set_key, r.feature_set_version, r.feature_key,
		       d.value_type, d.max_age_seconds
		FROM requested r
		JOIN %s d
		  ON d.feature_set_key=r.feature_set_key
		 AND d.feature_set_version=r.feature_set_version
		 AND d.feature_key=r.feature_key
		JOIN %s v
		  ON v.feature_set_key=d.feature_set_key
		 AND v.feature_set_version=d.feature_set_version
		WHERE v.status IN ('published', 'deprecated')
		ORDER BY r.ordinal`, values.String(), r.table("feature_definitions"), r.table("feature_set_versions"))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	definitions := make([]ServingFeatureDefinition, 0, len(refs))
	for rows.Next() {
		var definition ServingFeatureDefinition
		var maxAgeSeconds int64
		if err := rows.Scan(
			&definition.Ref.FeatureSetKey,
			&definition.Ref.FeatureSetVersion,
			&definition.Ref.FeatureKey,
			&definition.ValueType,
			&maxAgeSeconds,
		); err != nil {
			return nil, err
		}
		definition.Ref.Alias = aliases[featureRefIdentity(definition.Ref)]
		definition.MaxAge = time.Duration(maxAgeSeconds) * time.Second
		definitions = append(definitions, definition)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(definitions) != len(refs) {
		return nil, InvalidStateError{Message: "one or more feature references are not published or do not exist"}
	}
	return definitions, nil
}
