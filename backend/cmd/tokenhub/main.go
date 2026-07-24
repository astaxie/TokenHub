package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"tokenhub/backend/internal/server"
)

func main() {
	loadDotEnv()

	addr := getenv("TOKENHUB_HTTP_ADDR", ":8080")
	config := server.ConfigFromEnv()
	if err := config.ValidateForStartup(); err != nil {
		log.Fatal(err)
	}

	store, err := server.OpenStoreWithConfig(config.DatabaseURL, config)
	if err != nil {
		log.Fatal(err)
	}
	if err := server.RunStartupBootstrap(context.Background(), store, config); err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           server.NewWithConfig(store, config).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("tokenhub backend listening on %s", addr)
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe()
	}()

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	select {
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
		return
	case <-signalCtx.Done():
	}

	shutdownTimeout := time.Duration(config.GracefulShutdownSeconds) * time.Second
	if shutdownTimeout <= 0 {
		shutdownTimeout = 150 * time.Second
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("tokenhub graceful shutdown failed: %v", err)
		_ = srv.Close()
	}
	if err := <-serveErr; err != nil && err != http.ErrServerClosed {
		log.Printf("tokenhub server stopped with error: %v", err)
	}
}

// loadDotEnv loads the .env file into environment variables from common locations.
// It uses godotenv.Load (not Overload), so existing system environment variables
// take precedence and are not overridden by .env.
func loadDotEnv() {
	candidates := []string{
		".env",         // running from the backend directory
		"backend/.env", // running from the repository root
		"../.env",      // running from a subdirectory such as backend/cmd
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if err := godotenv.Load(path); err != nil {
			log.Printf("[tokenhub] failed to load env file %s: %v", path, err)
			continue
		}
		log.Printf("[tokenhub] loaded environment from %s", path)
		return
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
