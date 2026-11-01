package riskwarehouse_test

import (
	"testing"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/riskwarehouse"
)

func TestResolveWarehouseRunStatusSucceedsOnlyWhenEveryBusinessTaskSucceeded(t *testing.T) {
	tests := []struct {
		name   string
		states []string
		want   string
	}{
		{"all succeeded", []string{"success", "success", "success"}, "succeeded"},
		{"quality failed", []string{"success", "failed", "upstream_failed"}, "failed"},
		{"task missing", []string{"success", "none", "none"}, "failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := riskwarehouse.ResolveWarehouseRunStatus(test.states); got != test.want {
				t.Fatalf("status=%s, want %s", got, test.want)
			}
		})
	}
}
