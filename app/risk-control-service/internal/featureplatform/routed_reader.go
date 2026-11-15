package featureplatform

import (
	"context"
	"fmt"
)

type ServingRouteResolver interface {
	GetServingRoute(context.Context, string, int) (ServingRoute, error)
}

// RoutedFeatureSnapshotReader keeps Feature Factors storage-independent. The
// control-plane route changes the latest-value source without changing Factor,
// Rule, Policy, or caller code. Historical reads always remain on History.
type RoutedFeatureSnapshotReader struct {
	routes    ServingRouteResolver
	candidate CurrentFeatureStore
	reference FeatureSnapshotReader
	shadow    *ShadowFeatureSnapshotReader
	online    *OnlineMonitoredFeatureSnapshotReader
}

func NewRoutedFeatureSnapshotReader(
	routes ServingRouteResolver,
	candidate CurrentFeatureStore,
	reference FeatureSnapshotReader,
	recorder ShadowComparisonRecorder,
) *RoutedFeatureSnapshotReader {
	return &RoutedFeatureSnapshotReader{
		routes: routes, candidate: candidate, reference: reference,
		shadow: NewShadowFeatureSnapshotReader(candidate, reference, recorder),
		online: NewOnlineMonitoredFeatureSnapshotReader(candidate, reference, recorder),
	}
}

func (r *RoutedFeatureSnapshotReader) ReadLatestBefore(
	ctx context.Context,
	request SnapshotReadRequest,
) ([]FeatureSnapshot, error) {
	if r == nil || r.routes == nil || r.candidate == nil || r.reference == nil {
		return nil, fmt.Errorf("serving routes, candidate and reference readers are required")
	}
	route, err := r.routes.GetServingRoute(ctx, request.FeatureSetKey, request.FeatureSetVersion)
	if err != nil {
		return nil, fmt.Errorf("resolve feature serving route: %w", err)
	}
	switch route.Mode {
	case ServingModeOnline:
		return r.online.ReadLatestBefore(ctx, request)
	case ServingModeShadow, "":
		return r.shadow.ReadLatestBefore(ctx, request)
	default:
		return nil, fmt.Errorf("unsupported feature serving mode %q", route.Mode)
	}
}

func (r *RoutedFeatureSnapshotReader) ReadAt(
	ctx context.Context,
	request SnapshotReadRequest,
) ([]FeatureSnapshot, error) {
	if r == nil || r.reference == nil {
		return nil, fmt.Errorf("reference reader is required")
	}
	return r.reference.ReadAt(ctx, request)
}
