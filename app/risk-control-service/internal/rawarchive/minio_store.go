package rawarchive

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/minio/minio-go/v7"
)

type MinIOObjectStore struct {
	client *minio.Client
	bucket string
}

func NewMinIOObjectStore(client *minio.Client, bucket string) (*MinIOObjectStore, error) {
	if client == nil || bucket == "" {
		return nil, fmt.Errorf("MinIO client and bucket are required")
	}
	return &MinIOObjectStore{client: client, bucket: bucket}, nil
}

func (s *MinIOObjectStore) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{})
}

func (s *MinIOObjectStore) PutIfAbsent(
	ctx context.Context,
	key string,
	body []byte,
	digest string,
	metadata map[string]string,
) (PutResult, error) {
	options := minio.PutObjectOptions{
		ContentType:  "application/json",
		UserMetadata: metadata,
	}
	options.SetMatchETagExcept("*")
	_, err := s.client.PutObject(
		ctx, s.bucket, key, bytes.NewReader(body), int64(len(body)), options,
	)
	if err == nil {
		return PutResult{State: PutStateCreated}, nil
	}
	response := minio.ToErrorResponse(err)
	if response.Code != "PreconditionFailed" && response.StatusCode != 412 {
		return PutResult{}, err
	}
	info, statErr := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if statErr != nil {
		return PutResult{}, fmt.Errorf("inspect existing raw object: %w", statErr)
	}
	existingDigest := info.Metadata.Get("X-Amz-Meta-Sha256")
	if existingDigest == "" {
		for key, value := range info.UserMetadata {
			if strings.EqualFold(key, "sha256") {
				existingDigest = value
				break
			}
		}
	}
	if existingDigest != digest {
		return PutResult{}, ErrObjectConflict
	}
	return PutResult{State: PutStateReplay}, nil
}
