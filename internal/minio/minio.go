package minio

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/lifecycle"
)

type Client struct {
	*minio.Client
	BucketName string
}

func Connect(ctx context.Context, endpoint, accessKey, secretKey string, useSSL bool, bucketName string) (*Client, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to minio: %w", err)
	}

	// Check if bucket exists, if not create it
	exists, err := client.BucketExists(ctx, bucketName)
	if err != nil {
		return nil, fmt.Errorf("failed to check if bucket exists: %w", err)
	}

	if !exists {
		err = client.MakeBucket(ctx, bucketName, minio.MakeBucketOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to create bucket: %w", err)
		}
		log.Printf("Created MinIO bucket: %s", bucketName)
	}

	log.Printf("Connected to MinIO: %s", endpoint)

	if err := configureReportLifecycle(ctx, client, bucketName); err != nil {
		log.Printf("Failed to configure MinIO report lifecycle: %v", err)
	} else {
		log.Printf("Configured MinIO lifecycle: reports/ expires after 31 days")
	}

	return &Client{
		Client:     client,
		BucketName: bucketName,
	}, nil
}

func configureReportLifecycle(ctx context.Context, client *minio.Client, bucketName string) error {
	const reportLifecycleRuleID = "expire-reports-after-31-days"

	config, err := client.GetBucketLifecycle(ctx, bucketName)
	if err != nil {
		config = lifecycle.NewConfiguration()
	}

	rules := make([]lifecycle.Rule, 0, len(config.Rules)+1)
	for _, rule := range config.Rules {
		if rule.ID != reportLifecycleRuleID {
			rules = append(rules, rule)
		}
	}
	rules = append(rules, lifecycle.Rule{
		ID:     reportLifecycleRuleID,
		Prefix: "reports/",
		Status: "Enabled",
		Expiration: lifecycle.Expiration{
			Days: lifecycle.ExpirationDays(31),
		},
	})

	config.Rules = rules
	return client.SetBucketLifecycle(ctx, bucketName, config)
}

func (c *Client) UploadFile(ctx context.Context, objectName string, reader io.Reader, size int64, contentType string) (minio.UploadInfo, error) {
	return c.PutObject(ctx, c.BucketName, objectName, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
}

func (c *Client) GetSignedURL(ctx context.Context, objectName string, expires time.Duration) (string, error) {
	url, err := c.PresignedGetObject(ctx, c.BucketName, objectName, expires, nil)
	if err != nil {
		return "", err
	}
	return url.String(), nil
}
