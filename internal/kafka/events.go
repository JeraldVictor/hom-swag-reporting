package kafka

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type ReportRequest struct {
	EventID            string                 `json:"event_id"`
	EventType          string                 `json:"event_type"`
	SchemaVersion      int                    `json:"schema_version"`
	JobID              primitive.ObjectID     `json:"job_id"`
	ReportKey          string                 `json:"report_key"`
	ReportVersion      int                    `json:"report_version"`
	ReportType         string                 `json:"report_type"`
	Format             string                 `json:"format"`
	RequestedBy        primitive.ObjectID     `json:"requested_by"`
	OfficeID           *primitive.ObjectID    `json:"office_id,omitempty"`
	Parameters         map[string]interface{} `json:"parameters"`
	DefinitionSnapshot map[string]interface{} `json:"definition_snapshot"`
	TraceID            string                 `json:"trace_id"`
	CreatedAt          time.Time              `json:"created_at"`
}

type ReportStatusEvent struct {
	EventID       string             `json:"event_id"`
	EventType     string             `json:"event_type"`
	SchemaVersion int                `json:"schema_version"`
	JobID         primitive.ObjectID `json:"job_id"`
	Status        string             `json:"status"`
	Stage         string             `json:"stage"`
	Percent       int                `json:"percent"`
	Message       string             `json:"message"`
	File          *FileEventMetadata `json:"file,omitempty"`
	Error         *JobErrorEvent     `json:"error,omitempty"`
	TraceID       string             `json:"trace_id"`
	CreatedAt     time.Time          `json:"created_at"`
}

type FileEventMetadata struct {
	Storage     string `json:"storage"`
	Bucket      string `json:"bucket"`
	ObjectKey   string `json:"object_key"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	ExpiresAt   string `json:"expires_at,omitempty"`
}

type JobErrorEvent struct {
	Code    string                 `json:"code"`
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
}
