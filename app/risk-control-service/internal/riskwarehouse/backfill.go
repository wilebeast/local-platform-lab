package riskwarehouse

import (
	"context"
	"fmt"
	"time"
)

type BackfillStatus string

const (
	BackfillRunning   BackfillStatus = "running"
	BackfillSucceeded BackfillStatus = "succeeded"
	BackfillFailed    BackfillStatus = "failed"
)

type BackfillPointStatus string

const (
	BackfillPending   BackfillPointStatus = "pending"
	BackfillPointRun  BackfillPointStatus = "running"
	BackfillPointOK   BackfillPointStatus = "succeeded"
	BackfillPointFail BackfillPointStatus = "failed"
)

type BackfillCommand struct {
	BackfillID string
	Start      time.Time
	End        time.Time
	Step       time.Duration
}

type BackfillSummary struct {
	Status         BackfillStatus
	TotalCount     int
	SucceededCount int
	FailedCount    int
	PendingCount   int
}

type BackfillStore interface {
	Start(context.Context, BackfillCommand, []time.Time) error
	PointStatuses(context.Context, string) (map[time.Time]BackfillPointStatus, error)
	SetPointStatus(context.Context, string, time.Time, BackfillPointStatus, string) error
	Finalize(context.Context, string, BackfillSummary) error
}

type TimepointRunner interface {
	Run(context.Context, string, time.Time) error
}

type BackfillCoordinator struct {
	store  BackfillStore
	runner TimepointRunner
}

func NewBackfillCoordinator(store BackfillStore, runner TimepointRunner) *BackfillCoordinator {
	return &BackfillCoordinator{store: store, runner: runner}
}

func PlanBackfill(start, end time.Time, step time.Duration) ([]time.Time, error) {
	if start.IsZero() || end.IsZero() || step <= 0 {
		return nil, fmt.Errorf("start, end and a positive step are required")
	}
	start = start.UTC()
	end = end.UTC()
	if end.Before(start) {
		return nil, fmt.Errorf("backfill end cannot precede start")
	}
	points := make([]time.Time, 0, int(end.Sub(start)/step)+1)
	for point := start; !point.After(end); point = point.Add(step) {
		points = append(points, point)
	}
	if points[len(points)-1] != end {
		return nil, fmt.Errorf("backfill range must be divisible by step")
	}
	return points, nil
}

func (c *BackfillCoordinator) Run(ctx context.Context, command BackfillCommand) (BackfillSummary, error) {
	if command.BackfillID == "" {
		return BackfillSummary{}, fmt.Errorf("backfill_id is required")
	}
	points, err := PlanBackfill(command.Start, command.End, command.Step)
	if err != nil {
		return BackfillSummary{}, err
	}
	if err := c.store.Start(ctx, command, points); err != nil {
		return BackfillSummary{}, err
	}
	statuses, err := c.store.PointStatuses(ctx, command.BackfillID)
	if err != nil {
		return BackfillSummary{}, err
	}
	for _, point := range points {
		if statuses[point] == BackfillPointOK {
			continue
		}
		if err := c.store.SetPointStatus(ctx, command.BackfillID, point, BackfillPointRun, ""); err != nil {
			return BackfillSummary{}, err
		}
		if runErr := c.runner.Run(ctx, command.BackfillID, point); runErr != nil {
			if err := c.store.SetPointStatus(ctx, command.BackfillID, point, BackfillPointFail, runErr.Error()); err != nil {
				return BackfillSummary{}, err
			}
			statuses[point] = BackfillPointFail
			summary := summarizeBackfill(points, statuses)
			if err := c.store.Finalize(ctx, command.BackfillID, summary); err != nil {
				return BackfillSummary{}, err
			}
			return summary, runErr
		}
		if err := c.store.SetPointStatus(ctx, command.BackfillID, point, BackfillPointOK, ""); err != nil {
			return BackfillSummary{}, err
		}
		statuses[point] = BackfillPointOK
	}
	summary := summarizeBackfill(points, statuses)
	if err := c.store.Finalize(ctx, command.BackfillID, summary); err != nil {
		return BackfillSummary{}, err
	}
	return summary, nil
}

func summarizeBackfill(points []time.Time, statuses map[time.Time]BackfillPointStatus) BackfillSummary {
	summary := BackfillSummary{Status: BackfillSucceeded, TotalCount: len(points)}
	for _, point := range points {
		switch statuses[point] {
		case BackfillPointOK:
			summary.SucceededCount++
		case BackfillPointFail:
			summary.FailedCount++
		default:
			summary.PendingCount++
		}
	}
	if summary.FailedCount > 0 {
		summary.Status = BackfillFailed
	} else if summary.PendingCount > 0 {
		summary.Status = BackfillRunning
	}
	return summary
}
