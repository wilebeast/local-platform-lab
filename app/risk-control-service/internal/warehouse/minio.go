package warehouse

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
)

type MinIOWarehouse struct {
	client          *minio.Client
	rawBucket       string
	warehouseBucket string
}

func NewMinIOWarehouse(client *minio.Client, rawBucket, warehouseBucket string) (*MinIOWarehouse, error) {
	if client == nil || rawBucket == "" || warehouseBucket == "" {
		return nil, fmt.Errorf("MinIO client, raw bucket and warehouse bucket are required")
	}
	return &MinIOWarehouse{client: client, rawBucket: rawBucket, warehouseBucket: warehouseBucket}, nil
}

func (w *MinIOWarehouse) EnsureWarehouseBucket(ctx context.Context) error {
	exists, err := w.client.BucketExists(ctx, w.warehouseBucket)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return w.client.MakeBucket(ctx, w.warehouseBucket, minio.MakeBucketOptions{})
}

func (w *MinIOWarehouse) LoadFundMovementPartition(ctx context.Context, eventDate string) ([]RawObject, error) {
	prefix := "raw/v1/event_type=funds.movement.recorded/event_date=" + eventDate + "/"
	objects := make([]RawObject, 0)
	for item := range w.client.ListObjects(ctx, w.rawBucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if item.Err != nil {
			return nil, item.Err
		}
		object, err := w.client.GetObject(ctx, w.rawBucket, item.Key, minio.GetObjectOptions{})
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(object)
		closeErr := object.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read raw object %s: %w", item.Key, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close raw object %s: %w", item.Key, closeErr)
		}
		info, err := w.client.StatObject(ctx, w.rawBucket, item.Key, minio.StatObjectOptions{})
		if err != nil {
			return nil, fmt.Errorf("stat raw object %s: %w", item.Key, err)
		}
		objects = append(objects, RawObject{
			Key:  item.Key,
			Body: body,
			Metadata: map[string]string{
				"ingested-at": metadataValue(info, "ingested-at"),
				"sha256":      metadataValue(info, "sha256"),
			},
		})
	}
	return objects, nil
}

func (w *MinIOWarehouse) PutIfAbsent(
	ctx context.Context,
	key string,
	content []byte,
	contentType string,
	digest string,
) (bool, error) {
	options := minio.PutObjectOptions{
		ContentType:  contentType,
		UserMetadata: map[string]string{"sha256": digest},
	}
	options.SetMatchETagExcept("*")
	_, err := w.client.PutObject(
		ctx, w.warehouseBucket, key, bytes.NewReader(content), int64(len(content)), options,
	)
	if err == nil {
		return false, nil
	}
	response := minio.ToErrorResponse(err)
	if response.Code != "PreconditionFailed" && response.StatusCode != 412 {
		return false, err
	}
	info, statErr := w.client.StatObject(ctx, w.warehouseBucket, key, minio.StatObjectOptions{})
	if statErr != nil {
		return false, fmt.Errorf("inspect existing warehouse object: %w", statErr)
	}
	if metadataValue(info, "sha256") != digest {
		return false, fmt.Errorf("warehouse object key belongs to different content")
	}
	return true, nil
}

func metadataValue(info minio.ObjectInfo, key string) string {
	if value := info.Metadata.Get("X-Amz-Meta-" + key); value != "" {
		return value
	}
	for metadataKey, value := range info.UserMetadata {
		if strings.EqualFold(metadataKey, key) {
			return value
		}
	}
	return ""
}
