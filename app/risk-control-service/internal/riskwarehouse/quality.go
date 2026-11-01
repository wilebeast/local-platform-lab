package riskwarehouse

import (
	"errors"
	"math"
	"time"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

func ValidateFundActivitySnapshot(snapshot warehouse.SubjectFundActivitySnapshot, expectedAsOf time.Time) error {
	if snapshot.AsOfMillis != expectedAsOf.UTC().UnixMilli() {
		return errors.New("feature_as_of_mismatch")
	}
	if snapshot.WindowEndMillis != snapshot.AsOfMillis ||
		snapshot.WindowEndMillis-snapshot.WindowStartMillis != (24*time.Hour).Milliseconds() {
		return errors.New("feature_window_invalid")
	}
	if snapshot.InflowMinor < 0 || snapshot.OutflowMinor < 0 || snapshot.SourceEventCount < 0 {
		return errors.New("feature_amount_negative")
	}
	if snapshot.OutflowRatioDefined != (snapshot.InflowMinor > 0) {
		return errors.New("feature_ratio_definition_invalid")
	}
	if snapshot.OutflowRatioDefined {
		expectedRatio := float64(snapshot.OutflowMinor) / float64(snapshot.InflowMinor)
		if math.Abs(snapshot.OutflowRatio-expectedRatio) > 1e-9 {
			return errors.New("feature_ratio_mismatch")
		}
	} else if snapshot.OutflowRatio != 0 {
		return errors.New("feature_ratio_mismatch")
	}
	return nil
}
