package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jeraldvictor/hom-swag/reporting/internal/config"
	"github.com/jeraldvictor/hom-swag/reporting/internal/jobs"
	"github.com/jeraldvictor/hom-swag/reporting/internal/kafka"
	"github.com/jeraldvictor/hom-swag/reporting/internal/minio"
	"github.com/jeraldvictor/hom-swag/reporting/internal/mongo"
	"github.com/jeraldvictor/hom-swag/reporting/internal/reports"
	"github.com/jeraldvictor/hom-swag/reporting/internal/reports/static"
	"github.com/joho/godotenv"
)

func main() {
	// Load environment variables from .env file if it exists
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Create a context that is cancelled when we receive a termination signal
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize components (Config, Mongo, Kafka, MinIO)
	cfg := config.Load()
	log.Println("Initializing HomSwag Reporting Service...")

	// Connect to MongoDB
	mongoClient, err := mongo.Connect(ctx, cfg.MongoDBURI, cfg.MongoDatabase)
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer mongoClient.Close(context.Background())

	// Connect to MinIO
	minioClient, err := minio.Connect(ctx, cfg.MinIOEndpoint, cfg.MinIOAccessKey, cfg.MinIOSecretKey, cfg.MinIOUseSSL, cfg.MinIOBucket)
	if err != nil {
		log.Fatalf("Failed to connect to MinIO: %v", err)
	}

	// Initialize Kafka
	brokers := kafka.ParseBrokers(cfg.KafkaBrokers)
	consumer := kafka.NewConsumer(brokers, cfg.RequestTopic, cfg.ConsumerGroup)
	defer consumer.Close()

	eventProducer := kafka.NewProducer(brokers, cfg.EventTopic)
	defer eventProducer.Close()

	deadLetterProducer := kafka.NewProducer(brokers, cfg.DeadLetterTopic)
	defer deadLetterProducer.Close()

	// Initialize Registry
	registry := reports.NewRegistry()
	registry.Register(&static.RiderCommissionExecutor{})

	// Initialize Worker
	worker := jobs.NewWorker(mongoClient, minioClient, consumer, eventProducer, deadLetterProducer, registry)

	// Start health check server
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		// Basic health check - could be expanded to check mongo/kafka/minio
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: mux,
	}

	go func() {
		log.Printf("Health check server starting on port %s", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Health check server failed: %v", err)
		}
	}()

	// Start Worker
	go worker.Start(ctx)

	log.Println("Reporting Service is running. Press Ctrl+C to exit.")

	// Wait for termination signal
	<-ctx.Done()

	log.Println("Shutting down Reporting Service...")

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Reporting Service stopped.")
}
