package config

import (
	"fmt"

	"github.com/spf13/viper"
)

// Config represents the configuration of the bot.
type Config struct {
	Document           string `mapstructure:"DOCUMENT"`
	FIAUrl             string `mapstructure:"FIA_URL"`
	ThreadsAccessToken string `mapstructure:"THREADS_ACCESS_TOKEN"`
	ThreadsUserID      string `mapstructure:"THREADS_USER_ID"`
	ImgurClientID      string `mapstructure:"IMGUR_CLIENT_ID"`
	ScrapeInterval     int    `mapstructure:"SCRAPE_INTERVAL"`
	GeminiAPIKey       string `mapstructure:"GEMINI_API_KEY"`
}

// Load loads the configuration from environment variables and .env file.
func Load() (*Config, error) {
	viper.SetConfigFile(".env")
	viper.AddConfigPath(".")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		fmt.Printf("Error reading config file: %s\n", err)
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Set default values
	viper.SetDefault("DOCUMENT", "file.json")
	viper.SetDefault("SCRAPE_INTERVAL", 30)

	// Validate required fields
	if cfg.ThreadsAccessToken == "" {
		return nil, fmt.Errorf("THREADS_ACCESS_TOKEN is required")
	}
	if cfg.ThreadsUserID == "" {
		return nil, fmt.Errorf("THREADS_USER_ID is required")
	}
	if cfg.ImgurClientID == "" {
		return nil, fmt.Errorf("IMGUR_CLIENT_ID is required")
	}
	if cfg.GeminiAPIKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is required")
	}

	return &cfg, nil
}
