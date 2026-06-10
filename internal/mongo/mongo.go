package mongo

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Client struct {
	*mongo.Client
	Database *mongo.Database
}

func Connect(ctx context.Context, uri, dbName string) (*Client, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	clientOpts := options.Client().ApplyURI(uri)
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to mongodb: %w", err)
	}

	err = client.Ping(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to ping mongodb: %w", err)
	}

	log.Printf("Connected to MongoDB: %s", dbName)

	return &Client{
		Client:   client,
		Database: client.Database(dbName),
	}, nil
}

func (c *Client) Close(ctx context.Context) error {
	return c.Client.Disconnect(ctx)
}
