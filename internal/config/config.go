package config

import (
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port            string
	TempDir         string
	MongoDBURI      string
	MongoDatabase   string
	KafkaBrokers    string
	RequestTopic    string
	EventTopic      string
	DeadLetterTopic string
	ConsumerGroup   string
	MinIOEndpoint   string
	MinIOAccessKey  string
	MinIOSecretKey  string
	MinIOUseSSL     bool
	MinIOBucket     string
	MaxRows         int
	JobTTLDays      int
	SignedURLTTL    int
	EnableOTEL      bool
	OTELTracesURL   string
	OTELServiceName string
}

func Load() *Config {
	return &Config{
		Port:            getEnv("PORT", "3003"),
		TempDir:         getEnv("REPORTING_TEMP_DIR", "/tmp/reports"),
		MongoDBURI:      getEnv("MONGODB_URI", "mongodb://admin:password@127.0.0.1:27017/homswag?authSource=admin"),
		MongoDatabase:   getEnv("MONGO_DATABASE", "homswag"),
		KafkaBrokers:    getEnv("KAFKA_BROKERS", "127.0.0.1:9094"),
		RequestTopic:    getEnv("REPORTING_REQUEST_TOPIC", "homswag.reporting.requests"),
		EventTopic:      getEnv("REPORTING_EVENT_TOPIC", "homswag.reporting.events"),
		DeadLetterTopic: getEnv("REPORTING_DEAD_LETTER_TOPIC", "homswag.reporting.dead-letter"),
		ConsumerGroup:   getEnv("REPORTING_CONSUMER_GROUP", "homswag-reporting-workers"),
		MinIOEndpoint:   getMinIOEndpoint(),
		MinIOAccessKey:  getEnv("MINIO_ACCESS_KEY", "minioadmin"),
		MinIOSecretKey:  getEnv("MINIO_SECRET_KEY", "minioadmin"),
		MinIOUseSSL:     getEnvBool("MINIO_USE_SSL", false),
		MinIOBucket:     getEnv("REPORTING_BUCKET", "reports"),
		MaxRows:         getEnvInt("REPORTING_MAX_ROWS", 200000),
		JobTTLDays:      getEnvInt("REPORTING_JOB_TTL_DAYS", 30),
		SignedURLTTL:    getEnvInt("REPORTING_SIGNED_URL_TTL_SECONDS", 900),
		EnableOTEL:      getEnvBool("ENABLE_OTEL", false),
		OTELTracesURL:   getEnv("OTEL_TRACES_ENDPOINT", "http://127.0.0.1:4318/v1/traces"),
		OTELServiceName: getEnv("OTEL_REPORTING_SERVICE_NAME", "reporting-service"),
	}
}

func getMinIOEndpoint() string {
	endpoint := getEnv("MINIO_ENDPOINT", "127.0.0.1:9000")
	endpoint = strings.TrimSpace(endpoint)
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Host != "" {
		endpoint = parsed.Host
	}
	endpoint = strings.TrimSuffix(endpoint, "/")

	if _, _, err := net.SplitHostPort(endpoint); err == nil {
		return endpoint
	}

	port := getEnv("MINIO_PORT", "")
	if port == "" {
		return endpoint
	}

	return net.JoinHostPort(endpoint, port)
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if value, ok := os.LookupEnv(key); ok {
		i, err := strconv.Atoi(value)
		if err != nil {
			log.Printf("Warning: environment variable %s is not an integer, using fallback %d", key, fallback)
			return fallback
		}
		return i
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if value, ok := os.LookupEnv(key); ok {
		b, err := strconv.ParseBool(value)
		if err != nil {
			log.Printf("Warning: environment variable %s is not a boolean, using fallback %t", key, fallback)
			return fallback
		}
		return b
	}
	return fallback
}
