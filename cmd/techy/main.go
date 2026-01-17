package main

import (
	"os"
	"time"

	"github.com/CREVIOS/revo/internal/config"
	"github.com/CREVIOS/revo/internal/server"
	"github.com/CREVIOS/revo/internal/worker"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	// Setup logging
	setupLogging()

	log.Info().Msg("Starting TechyBot...")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load configuration")
	}

	if len(os.Args) > 1 && os.Args[1] == "worker" {
		log.Info().Msg("Starting TechyBot worker...")
		if err := worker.Run(cfg); err != nil {
			log.Fatal().Err(err).Msg("Worker error")
		}
		return
	}

	// Create and start server
	srv, err := server.New(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create server")
	}

	if err := srv.Start(); err != nil {
		log.Fatal().Err(err).Msg("Server error")
	}
}

// setupLogging configures zerolog
func setupLogging() {
	// Set global log level
	logLevel := os.Getenv("LOG_LEVEL")
	switch logLevel {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	// Use console writer for development
	if os.Getenv("LOG_FORMAT") != "json" {
		log.Logger = log.Output(zerolog.ConsoleWriter{
			Out:        os.Stderr,
			TimeFormat: time.RFC3339,
		})
	}
}
