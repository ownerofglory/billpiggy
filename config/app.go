package config

import "fmt"

type BillPiggyAppConfig struct {
	ServerAddr                  string `env:"SERVER_ADDR" envDefault:"0.0.0.0:8080"`
	LogLevel                    string `env:"LOG_LEVEL" envDefault:"info"`
	Environment                 string `env:"APP_ENV" envDefault:"development"`
	DatabaseURL                 string `env:"DATABASE_URL"`
	JWTSecret                   string `env:"JWT_SECRET"`
	BootstrapSuperAdminEmail    string `env:"BOOTSTRAP_SUPER_ADMIN_EMAIL"`
	BootstrapSuperAdminPassword string `env:"BOOTSTRAP_SUPER_ADMIN_PASSWORD"`
	SMTPAddress                 string `env:"SMTP_ADDRESS"`
	SMTPUsername                string `env:"SMTP_USERNAME"`
	SMTPPassword                string `env:"SMTP_PASSWORD"`
	SMTPFrom                    string `env:"SMTP_FROM"`
}

// Validate rejects production configurations that could silently lose state or sign unsafe tokens.
func (c BillPiggyAppConfig) Validate() error {
	if c.Environment == "production" && c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required in production")
	}
	if len(c.JWTSecret) < 32 {
		return fmt.Errorf("JWT_SECRET must contain at least 32 bytes")
	}
	return nil
}
