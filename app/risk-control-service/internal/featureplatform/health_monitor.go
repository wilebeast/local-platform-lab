package featureplatform

import (
	"context"
	"fmt"
	"time"
)

type HealthRecommendation string

const HealthRecommendationRollback HealthRecommendation = "rollback"

type HealthAlertStatus string

const HealthAlertOpen HealthAlertStatus = "open"

type OnlineHealthCheckRequest struct {
	CheckID           string
	FeatureSetKey     string
	FeatureSetVersion int
	EvidenceStart     time.Time
	EvidenceEnd       time.Time
	BucketDuration    time.Duration
	Criteria          PromotionCriteria
}

type FeatureServingHealthAlert struct {
	AlertID           string
	CheckID           string
	FeatureSetKey     string
	FeatureSetVersion int
	RouteGeneration   int64
	Status            HealthAlertStatus
	ReasonCodes       []string
	Evidence          PromotionEvidence
	Criteria          PromotionCriteria
	RecommendedAction HealthRecommendation
	CreatedAt         time.Time
}

type OnlineHealthCheckResult struct {
	Monitored      bool
	Healthy        bool
	ResolvedAlerts int64
	Route          ServingRoute
	Assessment     PromotionAssessment
	Alert          *FeatureServingHealthAlert
}

type FeatureServingHealthAlertStore interface {
	RecordHealthAlert(context.Context, FeatureServingHealthAlert) (FeatureServingHealthAlert, error)
	ResolveOpenHealthAlerts(context.Context, string, int, int64, time.Time) (int64, error)
}

type OnlineFeatureServingHealthMonitor struct {
	routes   ServingRouteResolver
	evidence PromotionEvidenceReader
	alerts   FeatureServingHealthAlertStore
	now      func() time.Time
}

func NewOnlineFeatureServingHealthMonitor(
	routes ServingRouteResolver,
	evidence PromotionEvidenceReader,
	alerts FeatureServingHealthAlertStore,
) *OnlineFeatureServingHealthMonitor {
	return &OnlineFeatureServingHealthMonitor{
		routes: routes, evidence: evidence, alerts: alerts,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (m *OnlineFeatureServingHealthMonitor) Evaluate(
	ctx context.Context,
	request OnlineHealthCheckRequest,
) (OnlineHealthCheckResult, error) {
	if m == nil || m.routes == nil || m.evidence == nil || m.alerts == nil {
		return OnlineHealthCheckResult{}, fmt.Errorf("online health monitor dependencies are required")
	}
	validationRequest := PromotionRequest{
		RequestID: request.CheckID, FeatureSetKey: request.FeatureSetKey,
		FeatureSetVersion: request.FeatureSetVersion, EvidenceStart: request.EvidenceStart,
		EvidenceEnd: request.EvidenceEnd, BucketDuration: request.BucketDuration,
		Criteria: request.Criteria, RequestedBy: "online-health-monitor", Reason: "continuous online check",
	}
	if err := validatePromotionRequest(validationRequest); err != nil {
		return OnlineHealthCheckResult{}, err
	}
	route, err := m.routes.GetServingRoute(ctx, request.FeatureSetKey, request.FeatureSetVersion)
	if err != nil {
		return OnlineHealthCheckResult{}, fmt.Errorf("resolve feature serving route: %w", err)
	}
	result := OnlineHealthCheckResult{Route: route}
	if route.Mode != ServingModeOnline {
		return result, nil
	}
	result.Monitored = true
	evidence, err := m.evidence.SummarizePromotionEvidence(ctx, PromotionEvidenceQuery{
		FeatureSetKey: request.FeatureSetKey, FeatureSetVersion: request.FeatureSetVersion,
		Start: request.EvidenceStart, End: request.EvidenceEnd, BucketDuration: request.BucketDuration,
	})
	if err != nil {
		return OnlineHealthCheckResult{}, fmt.Errorf("summarize online health evidence: %w", err)
	}
	if err := validatePromotionEvidence(evidence); err != nil {
		return OnlineHealthCheckResult{}, err
	}
	assessment := assessPromotion(request.Criteria, evidence, m.now())
	assessment.AssessmentID = stableServingChangeID("health-check", request.CheckID, request.FeatureSetKey, request.FeatureSetVersion)
	result.Assessment = assessment
	result.Healthy = assessment.Eligible
	if result.Healthy {
		resolved, err := m.alerts.ResolveOpenHealthAlerts(
			ctx, request.FeatureSetKey, request.FeatureSetVersion, route.Generation, m.now(),
		)
		if err != nil {
			return OnlineHealthCheckResult{}, fmt.Errorf("resolve recovered feature serving alerts: %w", err)
		}
		result.ResolvedAlerts = resolved
		return result, nil
	}
	alert, err := m.alerts.RecordHealthAlert(ctx, FeatureServingHealthAlert{
		AlertID: stableServingChangeID(
			"health-alert", fmt.Sprintf("%s-generation-%d", request.CheckID, route.Generation),
			request.FeatureSetKey, request.FeatureSetVersion,
		),
		CheckID: request.CheckID, FeatureSetKey: request.FeatureSetKey,
		FeatureSetVersion: request.FeatureSetVersion, RouteGeneration: route.Generation,
		Status: HealthAlertOpen, ReasonCodes: append([]string(nil), assessment.Reasons...),
		Evidence: evidence, Criteria: request.Criteria,
		RecommendedAction: HealthRecommendationRollback, CreatedAt: m.now(),
	})
	if err != nil {
		return OnlineHealthCheckResult{}, fmt.Errorf("record feature serving health alert: %w", err)
	}
	result.Alert = &alert
	return result, nil
}
