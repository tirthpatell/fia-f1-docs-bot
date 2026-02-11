package config

import (
	"fmt"

	"github.com/spf13/viper"
)

// Config represents the configuration of the bot.
type Config struct {
	// Storage configuration
	DBHost     string `mapstructure:"DB_HOST"`
	DBPort     string `mapstructure:"DB_PORT"`
	DBUser     string `mapstructure:"DB_USER"`
	DBPassword string `mapstructure:"DB_PASSWORD"`
	DBName     string `mapstructure:"DB_NAME"`
	DBSSLMode  string `mapstructure:"DB_SSL_MODE"`

	// Other configuration
	FIAUrl              string `mapstructure:"FIA_URL"`
	ThreadsAccessToken  string `mapstructure:"THREADS_ACCESS_TOKEN"`
	ThreadsUserID       string `mapstructure:"THREADS_USER_ID"`
	ThreadsClientID     string `mapstructure:"THREADS_CLIENT_ID"`
	ThreadsClientSecret string `mapstructure:"THREADS_CLIENT_SECRET"`
	ThreadsRedirectURI  string `mapstructure:"THREADS_REDIRECT_URI"`
	ScrapeInterval      int    `mapstructure:"SCRAPE_INTERVAL"`
	GeminiAPIKey        string `mapstructure:"GEMINI_API_KEY"`
	PicsurAPI           string `mapstructure:"PICSUR_API"`
	PicsurURL           string `mapstructure:"PICSUR_URL"`
	ShortenerAPIKey     string `mapstructure:"SHORTENER_API_KEY"`
	ShortenerURL        string `mapstructure:"SHORTENER_URL"`

	// Logging configuration
	LogLevel     string `mapstructure:"LOG_LEVEL"`
	LogAddSource bool   `mapstructure:"LOG_ADD_SOURCE"`
	Environment  string `mapstructure:"ENVIRONMENT"`
	Version      string `mapstructure:"VERSION"`
}

// Load loads the configuration from environment variables and .env file.
func Load() (*Config, error) {
	viper.SetConfigFile(".env")
	viper.AddConfigPath(".")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		fmt.Printf("Error reading config file: %s\n", err)
	}

	// Set default values before unmarshalling so they take effect
	viper.SetDefault("SCRAPE_INTERVAL", 30)
	viper.SetDefault("DB_PORT", "5432")
	viper.SetDefault("DB_SSL_MODE", "disable")
	viper.SetDefault("LOG_LEVEL", "info")
	viper.SetDefault("LOG_ADD_SOURCE", false)
	viper.SetDefault("ENVIRONMENT", "production")
	viper.SetDefault("VERSION", "unknown")

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate required fields
	if cfg.ThreadsAccessToken == "" {
		return nil, fmt.Errorf("THREADS_ACCESS_TOKEN is required")
	}
	if cfg.ThreadsUserID == "" {
		return nil, fmt.Errorf("THREADS_USER_ID is required")
	}
	if cfg.ThreadsClientID == "" {
		return nil, fmt.Errorf("THREADS_CLIENT_ID is required")
	}
	if cfg.ThreadsClientSecret == "" {
		return nil, fmt.Errorf("THREADS_CLIENT_SECRET is required")
	}
	if cfg.ThreadsRedirectURI == "" {
		return nil, fmt.Errorf("THREADS_REDIRECT_URI is required")
	}
	if cfg.PicsurAPI == "" {
		return nil, fmt.Errorf("PICSUR_API is required")
	}
	if cfg.GeminiAPIKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is required")
	}
	if cfg.PicsurURL == "" {
		return nil, fmt.Errorf("PICSUR_URL is required")
	}
	if cfg.ShortenerAPIKey == "" {
		return nil, fmt.Errorf("SHORTENER_API_KEY is required")
	}
	if cfg.ShortenerURL == "" {
		return nil, fmt.Errorf("SHORTENER_URL is required")
	}

	// Validate PostgreSQL configuration
	if cfg.DBHost == "" {
		return nil, fmt.Errorf("DB_HOST is required")
	}
	if cfg.DBUser == "" {
		return nil, fmt.Errorf("DB_USER is required")
	}
	if cfg.DBPassword == "" {
		return nil, fmt.Errorf("DB_PASSWORD is required")
	}
	if cfg.DBName == "" {
		return nil, fmt.Errorf("DB_NAME is required")
	}

	return &cfg, nil
}
