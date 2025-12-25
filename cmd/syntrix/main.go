package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/services"
)

func main() {
	// 0. Parse Command Line Flags
	runAPI := flag.Bool("api", false, "Run API Gateway (REST + Realtime)")
	runCSP := flag.Bool("csp", false, "Run CSP Service")
	runQuery := flag.Bool("query", false, "Run Query Service")
	runTriggerEvaluator := flag.Bool("trigger-evaluator", false, "Run Trigger Evaluator Service")
	runTriggerWorker := flag.Bool("trigger-worker", false, "Run Trigger Worker Service")
	runAll := flag.Bool("all", false, "Run All Services")
	listenHost := flag.String("host", "", "Host to listen on for all services")
	flag.Parse()

	// Default to running all if no specific flags are provided or if --all is set
	if *runAll || (!*runAPI && !*runCSP && !*runQuery && !*runTriggerEvaluator && !*runTriggerWorker) {
		*runAPI = true
		*runCSP = true
		*runQuery = true
		*runTriggerEvaluator = true
		*runTriggerWorker = true
	}

	// 1. Load Configuration
	cfg := config.LoadConfig()
	log.Println("Starting Syntrix Services...")
	if *runAPI {
		log.Println("- API Gateway (REST + Realtime): Enabled")
	}
	if *runCSP {
		log.Println("- CSP Service: Enabled")
	}
	if *runQuery {
		log.Println("- Query Service: Enabled")
	}
	if *runTriggerEvaluator {
		log.Println("- Trigger Evaluator Service: Enabled")
	}
	if *runTriggerWorker {
		log.Println("- Trigger Worker Service: Enabled")
	}

	// 2. Initialize Service Manager
	opts := services.Options{
		RunAPI:              *runAPI,
		RunCSP:              *runCSP,
		RunQuery:            *runQuery,
		RunTriggerEvaluator: *runTriggerEvaluator,
		RunTriggerWorker:    *runTriggerWorker,
		ListenHost:          *listenHost,
	}
	mgr := services.NewManager(cfg, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mgr.Init(ctx); err != nil {
		log.Fatalf("Failed to initialize services: %v", err)
	}

	// 3. Start Services
	// Context for background tasks
	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	mgr.Start(bgCtx)

	// 4. Wait for Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down services...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// Cancel background tasks first
	bgCancel()

	mgr.Shutdown(shutdownCtx)

	log.Println("All services stopped.")
}
