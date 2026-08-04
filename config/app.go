// Package config reads and validates the application's runtime configuration.
package config

import "fmt"

// BillPiggyAppConfig is the process configuration, read entirely from the
// environment so the same image runs in every environment.
type BillPiggyAppConfig struct {
	// ServerAddr is the address the HTTP server listens on.
	ServerAddr string `env:"SERVER_ADDR" envDefault:"0.0.0.0:8080"`
	// LogLevel sets the structured logger's minimum level.
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
	// Environment selects environment-specific behaviour such as secure cookies.
	Environment string `env:"APP_ENV" envDefault:"development"`
	// DatabaseURL points at PostgreSQL. Without it the app runs entirely
	// in-memory, which is intended for local development only.
	DatabaseURL string `env:"DATABASE_URL"`
	// JWTSecret signs access tokens and must contain at least 32 bytes.
	JWTSecret string `env:"JWT_SECRET"`
	// BootstrapSuperAdminEmail and BootstrapSuperAdminPassword create the first
	// super-admin on initial startup, since users cannot self-register.
	BootstrapSuperAdminEmail    string `env:"BOOTSTRAP_SUPER_ADMIN_EMAIL"`
	BootstrapSuperAdminPassword string `env:"BOOTSTRAP_SUPER_ADMIN_PASSWORD"`
	// SMTPAddress enables the email worker when set; the remaining SMTP fields
	// configure the connection it uses.
	SMTPAddress  string `env:"SMTP_ADDRESS"`
	SMTPUsername string `env:"SMTP_USERNAME"`
	SMTPPassword string `env:"SMTP_PASSWORD"`
	SMTPFrom     string `env:"SMTP_FROM"`
	// MinIOEndpoint selects the S3-compatible object store. Without it uploads
	// are held in memory, which is intended for local development only.
	MinIOEndpoint  string `env:"MINIO_ENDPOINT"`
	MinIOAccessKey string `env:"MINIO_ACCESS_KEY"`
	MinIOSecretKey string `env:"MINIO_SECRET_KEY"`
	// MinIOBucket holds receipts, profile images and generated reports.
	MinIOBucket string `env:"MINIO_BUCKET" envDefault:"billpiggy"`
	// MinIOUseSSL selects HTTPS for object-store traffic.
	MinIOUseSSL bool `env:"MINIO_USE_SSL" envDefault:"false"`
	// OpenAIAPIKey enables the AI features. Without it they report the provider
	// as unavailable rather than failing the request.
	OpenAIAPIKey string `env:"OPENAI_API_KEY"`
	// OpenAIAssistantModel is the conversational model used by the assistant.
	OpenAIAssistantModel string `env:"OPENAI_ASSISTANT_MODEL" envDefault:"gpt-5.6-luna"`
	// OpenAIBaseURL overrides the API endpoint, for routing through a
	// compatible gateway. Empty uses the provider's own endpoint.
	OpenAIBaseURL string `env:"OPENAI_BASE_URL"`
}

// Validate rejects production configurations that could silently lose state or sign unsafe tokens.
func (c BillPiggyAppConfig) Validate() error {
	if c.Environment == "production" && c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required in production")
	}
	if c.Environment == "production" && (c.MinIOEndpoint == "" || c.MinIOAccessKey == "" || c.MinIOSecretKey == "") {
		return fmt.Errorf("MinIO configuration is required in production")
	}
	if len(c.JWTSecret) < 32 {
		return fmt.Errorf("JWT_SECRET must contain at least 32 bytes")
	}
	return nil
}
