package riskwarehouse

import (
	"context"
	"fmt"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

type ClickHouseSnapshotReader struct {
	client *warehouse.ClickHouse
}

func NewClickHouseSnapshotReader(client *warehouse.ClickHouse) (*ClickHouseSnapshotReader, error) {
	if client == nil {
		return nil, fmt.Errorf("ClickHouse client is required")
	}
	return &ClickHouseSnapshotReader{client: client}, nil
}

func (r *ClickHouseSnapshotReader) ReadFundActivitySnapshot(
	ctx context.Context,
	query FeatureSnapshotQuery,
) (warehouse.SubjectFundActivitySnapshot, bool, error) {
	return r.client.ReadLatestFundActivitySnapshot(
		ctx,
		query.FeatureSetVersion,
		query.SubjectType,
		query.SubjectID,
		query.Currency,
		query.AsOf,
	)
}
