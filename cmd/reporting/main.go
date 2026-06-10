package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/config"
	"github.com/JeraldVictor/hom-swag-reporting/internal/jobs"
	"github.com/JeraldVictor/hom-swag-reporting/internal/kafka"
	"github.com/JeraldVictor/hom-swag-reporting/internal/minio"
	"github.com/JeraldVictor/hom-swag-reporting/internal/mongo"
	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"github.com/JeraldVictor/hom-swag-reporting/internal/reports/static"
	"github.com/joho/godotenv"
)

type PreviewRequest struct {
	ReportKey  string                 `json:"report_key"`
	Version    int                    `json:"version"`
	OfficeID   string                 `json:"office_id,omitempty"`
	Parameters map[string]interface{} `json:"parameters"`
	Limit      int                    `json:"limit"`
}

type PreviewResponse struct {
	Rows [][]interface{} `json:"rows"`
}

type MemorySink struct {
	Rows [][]interface{}
}

func (s *MemorySink) WriteRow(row []interface{}) error {
	s.Rows = append(s.Rows, row)
	return nil
}

func main() {
	// Load environment variables from .env file if it exists
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	// Initialize components (Config, Mongo, Kafka, MinIO)
	cfg := config.Load()
	log.Println("Initializing HomSwag Reporting Service...")

	// Create a context that is cancelled when we receive a termination signal
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
	registry.Register(static.NewRiderCommissionExecutor(mongoClient.Database))
	registry.Register(static.NewBeauticianCommissionExecutor(mongoClient.Database))
	registry.Register(static.NewPetrolWeeklyExecutor(mongoClient.Database))
	registry.Register(static.NewDailySalesExecutor(mongoClient.Database))
	registry.Register(static.NewStaffSummaryExecutor(mongoClient.Database))

	// Initialize Worker
	worker := jobs.NewWorker(mongoClient, minioClient, consumer, eventProducer, deadLetterProducer, registry)

	// Start health check and preview server
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	mux.HandleFunc("/preview", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req PreviewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Limit <= 0 {
			req.Limit = 100
		}

		executor, ok := registry.Get(req.ReportKey, req.Version)
		if !ok {
			http.Error(w, "Executor not found", http.StatusNotFound)
			return
		}

		parameters := make(map[string]interface{}, len(req.Parameters)+1)
		for key, value := range req.Parameters {
			parameters[key] = value
		}
		if req.OfficeID != "" {
			parameters["office_id"] = req.OfficeID
		}

		reportReq := reports.Request{
			ReportKey:  req.ReportKey,
			Version:    req.Version,
			Parameters: parameters,
			Limit:      req.Limit,
		}

		sink := &MemorySink{Rows: make([][]interface{}, 0)}
		if err := executor.Run(r.Context(), reportReq, sink); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PreviewResponse{Rows: sink.Rows})
	})

	server := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: mux,
	}

	go func() {
		log.Printf("Server starting on port %s", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
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
