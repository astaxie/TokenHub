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
	"tokenhub/backend/internal/dbcli"
	"tokenhub/backend/internal/server"
)

var (
	buildVersion   = server.DefaultAppVersion
	buildType      = "source"
	deploymentType = "source"
)

func main() {
	loadDotEnv()

	if len(os.Args) > 1 && os.Args[1] == "db" {
		dbcli.AppRelease = buildVersion
		os.Exit(dbcli.Run(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
	}

	addr := getenv("TOKENHUB_HTTP_ADDR", ":8080")
	config := server.ConfigFromEnv()
	config.AppVersion = buildVersion
	config.BuildType = buildType
	config.DeploymentType = deploymentType
	if runtimeDeploymentType := os.Getenv("TOKENHUB_DEPLOYMENT_TYPE"); runtimeDeploymentType != "" {
		config.DeploymentType = runtimeDeploymentType
	}
	if err := config.ValidateForStartup(); err != nil {
		log.Fatal(err)
	}

	// The managed-upgrade contract in docs/database-evolution.md keeps the
	// target release guarded until its first boot runs the auto-rollback guard
	// before any schema flow — a crashed boot
	// re-activates the previous release once, breaking a crash loop before the
	// new release touches the database.
	guardCtx, cancelGuard := context.WithTimeout(context.Background(), 60*time.Second)
	if err := server.RunStartupGuard(guardCtx, config); err != nil {
		cancelGuard()
		log.Fatalf("startup guard failed: %v", err)
	}
	cancelGuard()

	store, err := server.OpenStoreWithConfig(config.DatabaseURL, config)
	if err != nil {
		log.Fatal(err)
	}
	if err := server.RunStartupBootstrap(context.Background(), store, config); err != nil {
		log.Fatal(err)
	}
	// The target release completed both the schema flow and the startup
	// bootstrap; settle the pending upgrade so a later boot never auto-rolls
	// back a healthy release. Settling only after bootstrap keeps the
	// one-shot rollback armed for boots that fail before serving.
	if err := server.RecordStartupGuardSuccess(config); err != nil {
		log.Fatalf("record startup guard success: %v", err)
	}

	app := server.NewWithConfig(store, config)
	app.StartBillingScheduler()
	catalogInitCtx, cancelCatalogInit := context.WithTimeout(context.Background(), 30*time.Second)
	if initialized, initErr := app.InitializeProviderCatalog(catalogInitCtx); initErr != nil {
		log.Printf("[tokenhub] provider catalog initialization failed; using database snapshot: %v", initErr)
	} else if initialized {
		log.Printf("[tokenhub] provider catalog database snapshot refreshed from local catalog")
	}
	cancelCatalogInit()
	srv := &http.Server{
		Addr:              addr,
		Handler:           app.Handler(),
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
	if err := app.Shutdown(shutdownCtx); err != nil {
		log.Printf("tokenhub image worker shutdown failed: %v", err)
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
