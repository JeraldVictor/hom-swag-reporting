package jobs

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type JobStatus string

const (
	StatusQueued     JobStatus = "QUEUED"
	StatusProcessing JobStatus = "PROCESSING"
	StatusCompleted  JobStatus = "COMPLETED"
	StatusFailed     JobStatus = "FAILED"
	StatusCancelled  JobStatus = "CANCELLED"
	StatusExpired    JobStatus = "EXPIRED"
)

type ProgressStage string

const (
	StageQueued    ProgressStage = "queued"
	StageLoading   ProgressStage = "loading"
	StageQuerying  ProgressStage = "querying"
	StageRendering ProgressStage = "rendering"
	StageUploading ProgressStage = "uploading"
	StageCompleted ProgressStage = "completed"
	StageFailed    ProgressStage = "failed"
)

type ReportJob struct {
	ID                   primitive.ObjectID `bson:"_id,omitempty"`
	JobNumber            string             `bson:"job_number"`
	ReportKey            string             `bson:"report_key"`
	ReportVersion        int                `bson:"report_version"`
	ReportName           string             `bson:"report_name"`
	ReportType           string             `bson:"report_type"`
	RequestedBy          primitive.ObjectID `bson:"requested_by"`
	OfficeID             *primitive.ObjectID `bson:"office_id,omitempty"`
	Parameters           map[string]interface{} `bson:"parameters"`
	NormalizedParameters map[string]interface{} `bson:"normalized_parameters"`
	Format               string             `bson:"format"`
	Status               JobStatus          `bson:"status"`
	Progress             Progress           `bson:"progress"`
	AttemptCount         int                `bson:"attempt_count"`
	MaxAttempts          int                `bson:"max_attempts"`
	IdempotencyKey       string             `bson:"idempotency_key"`
	File                 *FileMetadata      `bson:"file,omitempty"`
	Error                *JobError          `bson:"error,omitempty"`
	StartedAt            *time.Time         `bson:"started_at,omitempty"`
	CompletedAt          *time.Time         `bson:"completed_at,omitempty"`
	ExpiresAt            *time.Time         `bson:"expires_at,omitempty"`
	CreatedAt            time.Time          `bson:"created_at"`
	UpdatedAt            time.Time          `bson:"updated_at"`
}

type Progress struct {
	Percent int           `bson:"percent"`
	Stage   ProgressStage `bson:"stage"`
	Message string        `bson:"message"`
}

type FileMetadata struct {
	Storage     string    `bson:"storage"`
	Bucket      string    `bson:"bucket"`
	ObjectKey   string    `bson:"object_key"`
	Filename    string    `bson:"filename"`
	ContentType string    `bson:"content_type"`
	SizeBytes   int64     `bson:"size_bytes"`
	Checksum    string    `bson:"checksum,omitempty"`
	ExpiresAt   *time.Time `bson:"expires_at,omitempty"`
}

type JobError struct {
	Code    string                 `bson:"code"`
	Message string                 `bson:"message"`
	Details map[string]interface{} `bson:"details,omitempty"`
}
