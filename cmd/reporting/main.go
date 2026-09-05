package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/config"
	"github.com/JeraldVictor/hom-swag-reporting/internal/earnings"
	"github.com/JeraldVictor/hom-swag-reporting/internal/fieldreports"
	"github.com/JeraldVictor/hom-swag-reporting/internal/fieldsettlements"
	"github.com/JeraldVictor/hom-swag-reporting/internal/jobs"
	"github.com/JeraldVictor/hom-swag-reporting/internal/kafka"
	"github.com/JeraldVictor/hom-swag-reporting/internal/leaderboard"
	"github.com/JeraldVictor/hom-swag-reporting/internal/minio"
	"github.com/JeraldVictor/hom-swag-reporting/internal/mongo"
	"github.com/JeraldVictor/hom-swag-reporting/internal/observability"
	"github.com/JeraldVictor/hom-swag-reporting/internal/render"
	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"github.com/JeraldVictor/hom-swag-reporting/internal/reports/static"
	"github.com/joho/godotenv"
)

type PreviewRequest struct {
	ReportKey       string                 `json:"report_key"`
	Version         int                    `json:"version"`
	Format          string                 `json:"format,omitempty"`
	OfficeID        string                 `json:"office_id,omitempty"`
	Parameters      map[string]interface{} `json:"parameters"`
	Limit           *int                   `json:"limit,omitempty"`
	SelectedColumns []string               `json:"selected_columns,omitempty"`
	AllowSensitive  bool                   `json:"allow_sensitive_columns,omitempty"`
}

type PreviewResponse struct {
	Rows [][]interface{} `json:"rows"`
}

type DefinitionResponse struct {
	ReportKey      string           `json:"report_key"`
	Version        int              `json:"version"`
	Columns        []reports.Column `json:"columns"`
	Filters        []reports.Filter `json:"filters"`
	AllowedFormats []string         `json:"allowed_formats"`
	DefaultFormat  string           `json:"default_format"`
}

type SummaryResponse struct {
	ReportKey string             `json:"report_key"`
	Version   int                `json:"version"`
	RowCount  int                `json:"row_count"`
	Totals    map[string]float64 `json:"totals"`
}

type MemorySink struct {
	Rows [][]interface{}
}

func (s *MemorySink) WriteRow(row []interface{}) error {
	s.Rows = append(s.Rows, row)
	return nil
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, X-Office-ID")
			w.Header().Set("Access-Control-Expose-Headers", "Content-Disposition")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withBearerAuth(next http.Handler) http.Handler {
	serviceToken := strings.TrimSpace(os.Getenv("REPORTING_API_TOKEN"))
	jwtSecret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/earnings/") {
			next.ServeHTTP(w, r)
			return
		}

		authorization := r.Header.Get("Authorization")
		if serviceToken != "" && authorization == "Bearer "+serviceToken {
			next.ServeHTTP(w, r)
			return
		}
		if _, err := earnings.VerifyAdminToken(authorization, jwtSecret); err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func buildReportRequest(req PreviewRequest) reports.Request {
	parameters := make(map[string]interface{}, len(req.Parameters)+1)
	for key, value := range req.Parameters {
		parameters[key] = value
	}
	if req.OfficeID != "" {
		parameters["office_id"] = req.OfficeID
	}

	limit := 0
	if req.Limit != nil {
		limit = *req.Limit
	}

	return reports.Request{
		ReportKey:       req.ReportKey,
		Version:         req.Version,
		Parameters:      parameters,
		Limit:           limit,
		SelectedColumns: req.SelectedColumns,
		AllowSensitive:  req.AllowSensitive,
	}
}

func normalizePreviewLimit(limit *int) (int, error) {
	if limit == nil {
		return 100, nil
	}
	if *limit < 0 {
		return 0, fmt.Errorf("limit must be zero or greater")
	}
	return *limit, nil
}

func runInMemoryReport(ctx context.Context, executor reports.Executor, req reports.Request) (*MemorySink, error) {
	sink := &MemorySink{Rows: make([][]interface{}, 0)}
	if err := executor.Validate(ctx, req); err != nil {
		return nil, err
	}

	projectedSink, err := reports.NewProjectionSinkWithOptions(executor, req.SelectedColumns, sink, reports.ProjectionOptions{
		AllowSensitive: req.AllowSensitive,
	})
	if err != nil {
		return nil, err
	}

	if err := executor.Run(ctx, req, projectedSink); err != nil {
		return nil, err
	}
	return sink, nil
}

func runRawInMemoryReport(ctx context.Context, executor reports.Executor, req reports.Request) (*MemorySink, error) {
	sink := &MemorySink{Rows: make([][]interface{}, 0)}
	if err := executor.Validate(ctx, req); err != nil {
		return nil, err
	}

	if _, err := reports.NewProjectionSinkWithOptions(executor, req.SelectedColumns, &MemorySink{}, reports.ProjectionOptions{
		AllowSensitive: req.AllowSensitive,
	}); err != nil {
		return nil, err
	}

	rawReq := req
	rawReq.SelectedColumns = nil
	if err := executor.Run(ctx, rawReq, sink); err != nil {
		return nil, err
	}
	return sink, nil
}

func summarizeRows(executor reports.Executor, selectedColumns []string, rows [][]interface{}) SummaryResponse {
	response := SummaryResponse{
		ReportKey: executor.Key(),
		Version:   executor.Version(),
		Totals:    map[string]float64{},
	}
	if len(rows) <= 1 {
		return response
	}

	columns := []reports.Column{}
	if provider, ok := executor.(reports.ColumnProvider); ok {
		columns = provider.Columns()
	}
	indexes := make([]int, 0, len(columns))
	if len(selectedColumns) > 0 {
		indexByKey := map[string]int{}
		for index, column := range columns {
			indexByKey[column.Key] = index
		}
		for _, key := range selectedColumns {
			if index, ok := indexByKey[key]; ok {
				indexes = append(indexes, index)
			}
		}
	} else {
		for index := range columns {
			indexes = append(indexes, index)
		}
	}

	dataRows := rows[1:]
	if len(dataRows) > 0 && len(dataRows[len(dataRows)-1]) > 0 && dataRows[len(dataRows)-1][0] == "Total" {
		dataRows = dataRows[:len(dataRows)-1]
	}
	response.RowCount = len(dataRows)

	for _, row := range dataRows {
		for _, index := range indexes {
			if index >= len(columns) || !columns[index].ContributesToTotal {
				continue
			}
			if index >= len(row) {
				continue
			}
			cell := row[index]
			switch value := cell.(type) {
			case int:
				response.Totals[columns[index].Key] += float64(value)
			case int64:
				response.Totals[columns[index].Key] += float64(value)
			case float64:
				response.Totals[columns[index].Key] += value
			case string:
				if parsed, err := strconv.ParseFloat(value, 64); err == nil {
					response.Totals[columns[index].Key] += parsed
				}
			}
		}
	}
	return response
}

func exportReport(ctx context.Context, executor reports.Executor, req reports.Request, title string) ([]byte, string, string, error) {
	format := strings.ToUpper(req.Format)
	if format == "" {
		format = "CSV"
	}

	var out bytes.Buffer
	var sink reports.RowSink
	var contentType string
	var extension string
	var xlsxWriter *render.XLSXWriter
	var pdfWriter *render.PDFWriter

	switch format {
	case "CSV":
		csvWriter := render.NewCSVWriter(&out)
		sink = csvWriter
		contentType = "text/csv"
		extension = "csv"
	case "XLSX":
		xlsxWriter = render.NewXLSXWriter()
		defer xlsxWriter.Close()
		sink = xlsxWriter
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		extension = "xlsx"
	case "PDF":
		pdfWriter = render.NewPDFWriter()
		sink = pdfWriter
		contentType = "application/pdf"
		extension = "pdf"
	default:
		return nil, "", "", strconv.ErrSyntax
	}

	if err := executor.Validate(ctx, req); err != nil {
		return nil, "", "", err
	}
	projectedSink, err := reports.NewProjectionSinkWithOptions(executor, req.SelectedColumns, sink, reports.ProjectionOptions{
		AllowSensitive: req.AllowSensitive,
	})
	if err != nil {
		return nil, "", "", err
	}
	if err := executor.Run(ctx, req, projectedSink); err != nil {
		return nil, "", "", err
	}

	switch format {
	case "CSV":
		csvWriter := sink.(*render.CSVWriter)
		csvWriter.Flush()
		if err := csvWriter.Error(); err != nil {
			return nil, "", "", err
		}
	case "XLSX":
		if err := xlsxWriter.Write(&out); err != nil {
			return nil, "", "", err
		}
	case "PDF":
		if err := pdfWriter.WriteTo(&out, title); err != nil {
			return nil, "", "", err
		}
	}

	return out.Bytes(), contentType, extension, nil
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

	shutdownTelemetry := observability.Init(ctx, observability.Config{
		Enabled:     cfg.EnableOTEL,
		TracesURL:   cfg.OTELTracesURL,
		ServiceName: cfg.OTELServiceName,
		Environment: os.Getenv("DEPLOYMENT_ENVIRONMENT"),
	})
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTelemetry(shutdownCtx); err != nil {
			log.Printf("Failed to shutdown telemetry: %v", err)
		}
	}()

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
	earningsSourceConsumer := kafka.NewConsumer(brokers, cfg.EarningsSourceTopic, cfg.EarningsSourceConsumerGroup)
	defer earningsSourceConsumer.Close()

	eventProducer := kafka.NewProducer(brokers, cfg.EventTopic)
	defer eventProducer.Close()

	deadLetterProducer := kafka.NewProducer(brokers, cfg.DeadLetterTopic)
	defer deadLetterProducer.Close()

	// Earnings mode is persisted per office. The environment value is only the
	// fallback for offices that have never been explicitly configured.
	earningsRepository := earnings.NewRepositoryWithOptions(mongoClient.Database, earnings.RepositoryOptions{
		DefaultMode:                 cfg.EarningsMode,
		AllowNonTransactionalWrites: cfg.AllowNonTransactionalWrites,
	})
	if cfg.AllowNonTransactionalWrites {
		log.Printf("WARNING: EARNINGS_ALLOW_NON_TRANSACTIONAL_WRITES is enabled; earnings writes are not transactionally atomic")
	}
	indexCtx, cancelIndexes := context.WithTimeout(ctx, 10*time.Second)
	if err := earningsRepository.EnsureIndexes(indexCtx); err != nil {
		cancelIndexes()
		log.Fatalf("Failed to ensure earnings indexes: %v", err)
	}
	cancelIndexes()

	// Initialize Registry
	registry := reports.NewRegistry()
	registry.Register(static.NewRiderCommissionExecutorWithModeProvider(mongoClient.Database, cfg.EarningsMode, earningsRepository))
	registry.Register(static.NewBeauticianCommissionExecutorWithModeProvider(mongoClient.Database, cfg.EarningsMode, earningsRepository))
	registry.Register(static.NewPetrolWeeklyExecutorWithModeProvider(mongoClient.Database, cfg.EarningsMode, earningsRepository))
	registry.Register(static.NewDailySalesExecutor(mongoClient.Database))
	registry.Register(static.NewStaffSummaryExecutor(mongoClient.Database))
	registry.Register(static.NewCODPendingExecutor(mongoClient.Database))
	registry.Register(static.NewCustomerBookingExecutor(mongoClient.Database))
	registry.Register(static.NewCustomerInformationExecutor(mongoClient.Database))
	registry.Register(static.NewProductInsightsExecutor(mongoClient.Database))

	// Initialize Worker
	worker := jobs.NewWorker(mongoClient, minioClient, cfg.TempDir, consumer, eventProducer, deadLetterProducer, registry)

	// Start health check and preview server
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	earningsAPI := earnings.NewAPI(earningsRepository, cfg.JWTSecret, cfg.EarningsMode)
	mux.Handle("/api/earnings/", earningsAPI.Handler())
	leaderboardAPI := leaderboard.NewAPI(leaderboard.NewService(leaderboard.NewMongoStore(mongoClient.Database)))
	mux.Handle("/leaderboard", leaderboardAPI.Handler())
	mux.Handle("/field-report-detail", fieldreports.NewAPI(earningsRepository).Handler())
	mux.Handle("/field-settlements", fieldsettlements.NewAPI(earningsRepository).Handler())

	// Source notifications contain identifiers and dates only. The worker
	// queues an idempotent rebuild which reloads persisted monetary snapshots
	// from Mongo before anything is written to the ledger.
	earningsSourceWorker := earnings.NewSourceEventWorker(earningsSourceConsumer, deadLetterProducer, earningsRepository)
	go earningsSourceWorker.Start(ctx)

	// Rebuild requests are persisted by the admin API and processed by the
	// reporting service itself. ClaimNextRebuild uses an atomic Mongo update,
	// so multiple service instances can safely poll the queue.
	earningsProcessor := earnings.NewProcessor(earningsRepository)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				processed, processErr := earningsProcessor.ProcessNext(ctx)
				if processErr != nil {
					log.Printf("earnings rebuild failed: %v", processErr)
				} else if processed {
					log.Printf("earnings rebuild completed")
				}
			}
		}
	}()

	mux.HandleFunc("/definitions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		definitions := make([]DefinitionResponse, 0)
		for _, executor := range registry.List() {
			definition := DefinitionResponse{
				ReportKey: executor.Key(),
				Version:   executor.Version(),
				Filters: []reports.Filter{
					{Key: "start_date", Label: "Start Date", Type: "date", Required: true},
					{Key: "end_date", Label: "End Date", Type: "date", Required: true},
				},
				AllowedFormats: []string{"CSV", "XLSX", "PDF"},
				DefaultFormat:  "CSV",
			}
			if provider, ok := executor.(reports.ColumnProvider); ok {
				definition.Columns = provider.Columns()
			}
			if provider, ok := executor.(reports.DefinitionProvider); ok {
				metadata := provider.Definition()
				definition.Filters = metadata.Filters
				definition.AllowedFormats = metadata.AllowedFormats
				definition.DefaultFormat = metadata.DefaultFormat
			}
			definitions = append(definitions, definition)
		}
		sort.Slice(definitions, func(i, j int) bool {
			if definitions[i].ReportKey == definitions[j].ReportKey {
				return definitions[i].Version < definitions[j].Version
			}
			return definitions[i].ReportKey < definitions[j].ReportKey
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(definitions)
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

		limit, err := normalizePreviewLimit(req.Limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		req.Limit = &limit

		executor, ok := registry.Get(req.ReportKey, req.Version)
		if !ok {
			http.Error(w, "Executor not found", http.StatusNotFound)
			return
		}

		sink, err := runInMemoryReport(r.Context(), executor, buildReportRequest(req))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PreviewResponse{Rows: sink.Rows})
	})

	mux.HandleFunc("/summary", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req PreviewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		executor, ok := registry.Get(req.ReportKey, req.Version)
		if !ok {
			http.Error(w, "Executor not found", http.StatusNotFound)
			return
		}

		sink, err := runRawInMemoryReport(r.Context(), executor, buildReportRequest(req))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(summarizeRows(executor, req.SelectedColumns, sink.Rows))
	})

	mux.HandleFunc("/export", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req PreviewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		executor, ok := registry.Get(req.ReportKey, req.Version)
		if !ok {
			http.Error(w, "Executor not found", http.StatusNotFound)
			return
		}

		reportReq := buildReportRequest(req)
		reportReq.Format = req.Format
		body, contentType, extension, err := exportReport(r.Context(), executor, reportReq, executor.Key())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		filename := executor.Key() + "_v" + strconv.Itoa(executor.Version()) + "." + extension
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	})

	server := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: withCORS(withBearerAuth(mux)),
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
