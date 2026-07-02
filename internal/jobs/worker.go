package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/kafka"
	"github.com/JeraldVictor/hom-swag-reporting/internal/minio"
	"github.com/JeraldVictor/hom-swag-reporting/internal/mongo"
	"github.com/JeraldVictor/hom-swag-reporting/internal/render"
	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Worker struct {
	MongoClient        *mongo.Client
	MinioClient        *minio.Client
	Consumer           *kafka.Consumer
	EventProducer      *kafka.Producer
	DeadLetterProducer *kafka.Producer
	Registry           *reports.Registry
}

func NewWorker(mc *mongo.Client, minioC *minio.Client, consumer *kafka.Consumer, ep *kafka.Producer, dlp *kafka.Producer, reg *reports.Registry) *Worker {
	return &Worker{
		MongoClient:        mc,
		MinioClient:        minioC,
		Consumer:           consumer,
		EventProducer:      ep,
		DeadLetterProducer: dlp,
		Registry:           reg,
	}
}

func (w *Worker) Start(ctx context.Context) {
	log.Println("Worker started, waiting for jobs...")
	for {
		select {
		case <-ctx.Done():
			return
		default:
			msg, err := w.Consumer.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("Error reading message: %v", err)
				continue
			}

			messageCtx, messageSpan := w.Consumer.StartMessageSpan(ctx, msg)
			var req kafka.ReportRequest
			if err := json.Unmarshal(msg.Value, &req); err != nil {
				log.Printf("Error unmarshaling request: %v", err)
				messageSpan.RecordError(err)
				// Send to dead letter
				w.DeadLetterProducer.SendMessage(messageCtx, string(msg.Key), msg.Value)
				messageSpan.End()
				continue
			}

			go func() {
				defer messageSpan.End()
				w.ProcessJob(messageCtx, req)
			}()
		}
	}
}

func (w *Worker) ProcessJob(ctx context.Context, req kafka.ReportRequest) {
	log.Printf("Processing job: %s (%s)", req.JobID.Hex(), req.ReportKey)

	// 1. Update status to PROCESSING
	w.sendStatus(ctx, req.JobID, "PROCESSING", "loading", 10, "Initializing report", nil, nil, req.TraceID)

	// 2. Find executor
	executor, ok := w.Registry.Get(req.ReportKey, req.ReportVersion)
	if !ok {
		err := fmt.Errorf("executor not found for %s v%d", req.ReportKey, req.ReportVersion)
		w.handleError(ctx, req.JobID, "EXECUTOR_NOT_FOUND", err.Error(), req.TraceID)
		return
	}

	// 3. Run report and stream to temp file
	tempDir, err := os.MkdirTemp("", "homswag-report-*")
	if err != nil {
		w.handleError(ctx, req.JobID, "TEMP_DIR_ERROR", err.Error(), req.TraceID)
		return
	}
	defer os.RemoveAll(tempDir)
	filename := fmt.Sprintf("%s_%s.%s", req.ReportKey, req.JobID.Hex(), strings.ToLower(req.Format))
	tempFilePath := filepath.Join(tempDir, filename)

	f, err := os.Create(tempFilePath)
	if err != nil {
		w.handleError(ctx, req.JobID, "TEMP_FILE_ERROR", err.Error(), req.TraceID)
		return
	}
	defer f.Close()
	defer os.Remove(tempFilePath)

	var sink reports.RowSink
	var contentType string
	var xlsxWriter *render.XLSXWriter
	var pdfWriter *render.PDFWriter

	if req.Format == "CSV" {
		csvWriter := render.NewCSVWriter(f)
		defer csvWriter.Flush()
		sink = csvWriter
		contentType = "text/csv"
	} else if req.Format == "XLSX" {
		xlsxWriter = render.NewXLSXWriter()
		defer xlsxWriter.Close()
		sink = xlsxWriter
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	} else if req.Format == "PDF" {
		pdfWriter = render.NewPDFWriter()
		sink = pdfWriter
		contentType = "application/pdf"
	} else {
		w.handleError(ctx, req.JobID, "UNSUPPORTED_FORMAT", "Only CSV, XLSX, and PDF are currently supported", req.TraceID)
		return
	}

	w.sendStatus(ctx, req.JobID, "PROCESSING", "querying", 30, "Executing query", nil, nil, req.TraceID)

	parameters := make(map[string]interface{}, len(req.Parameters)+1)
	for key, value := range req.Parameters {
		parameters[key] = value
	}
	if req.OfficeID != nil {
		parameters["office_id"] = req.OfficeID.Hex()
	}

	reportReq := reports.Request{
		ReportKey:       req.ReportKey,
		Version:         req.ReportVersion,
		Format:          req.Format,
		Parameters:      parameters,
		SelectedColumns: req.SelectedColumns,
	}

	if err := executor.Validate(ctx, reportReq); err != nil {
		w.handleError(ctx, req.JobID, "REPORT_VALIDATION_ERROR", err.Error(), req.TraceID)
		return
	}

	projectedSink, err := reports.NewProjectionSink(executor, req.SelectedColumns, sink)
	if err != nil {
		w.handleError(ctx, req.JobID, "REPORT_COLUMN_ERROR", err.Error(), req.TraceID)
		return
	}

	if err := executor.Run(ctx, reportReq, projectedSink); err != nil {
		w.handleError(ctx, req.JobID, "REPORT_RUN_ERROR", err.Error(), req.TraceID)
		return
	}

	if req.Format == "CSV" {
		sink.(*render.CSVWriter).Flush()
		if err := sink.(*render.CSVWriter).Error(); err != nil {
			w.handleError(ctx, req.JobID, "CSV_FLUSH_ERROR", err.Error(), req.TraceID)
			return
		}
	} else if req.Format == "XLSX" {
		if err := xlsxWriter.WriteTo(f); err != nil {
			w.handleError(ctx, req.JobID, "XLSX_WRITE_ERROR", err.Error(), req.TraceID)
			return
		}
	} else if req.Format == "PDF" {
		reportTitle := req.ReportKey
		if name, ok := req.DefinitionSnapshot["name"].(string); ok && name != "" {
			reportTitle = name
		}
		if err := pdfWriter.WriteTo(f, reportTitle); err != nil {
			w.handleError(ctx, req.JobID, "PDF_WRITE_ERROR", err.Error(), req.TraceID)
			return
		}
	}

	// 4. Upload to MinIO
	w.sendStatus(ctx, req.JobID, "PROCESSING", "uploading", 80, "Uploading to storage", nil, nil, req.TraceID)

	f.Seek(0, 0)
	stat, _ := f.Stat()

	now := time.Now()
	expiresAt := now.AddDate(0, 1, 0)
	objectKey := fmt.Sprintf("reports/%d/%02d/%s/%s", now.Year(), now.Month(), req.JobID.Hex(), filename)

	uploadInfo, err := w.MinioClient.UploadFile(ctx, objectKey, f, stat.Size(), contentType)
	if err != nil {
		w.handleError(ctx, req.JobID, "UPLOAD_ERROR", err.Error(), req.TraceID)
		return
	}

	fileMeta := &kafka.FileEventMetadata{
		Storage:     "minio",
		Bucket:      w.MinioClient.BucketName,
		ObjectKey:   objectKey,
		Filename:    filename,
		ContentType: contentType,
		SizeBytes:   uploadInfo.Size,
		ExpiresAt:   expiresAt.Format(time.RFC3339),
	}

	// 5. Update status to COMPLETED
	w.sendStatus(ctx, req.JobID, "COMPLETED", "completed", 100, "Report generated successfully", fileMeta, nil, req.TraceID)
	log.Printf("Job completed: %s", req.JobID.Hex())
}

func (w *Worker) sendStatus(ctx context.Context, jobID primitive.ObjectID, status, stage string, percent int, message string, file *kafka.FileEventMetadata, err *kafka.JobErrorEvent, traceID string) {
	event := kafka.ReportStatusEvent{
		EventID:       uuid.New().String(),
		EventType:     "report.status_changed",
		SchemaVersion: 1,
		JobID:         jobID,
		Status:        status,
		Stage:         stage,
		Percent:       percent,
		Message:       message,
		File:          file,
		Error:         err,
		TraceID:       traceID,
		CreatedAt:     time.Now(),
	}

	if err := w.EventProducer.SendMessage(ctx, jobID.Hex(), event); err != nil {
		log.Printf("Error sending status event: %v", err)
	}
}

func (w *Worker) handleError(ctx context.Context, jobID primitive.ObjectID, code, message, traceID string) {
	jobErr := &kafka.JobErrorEvent{
		Code:    code,
		Message: message,
	}
	w.sendStatus(ctx, jobID, "FAILED", "failed", 0, message, nil, jobErr, traceID)
}
