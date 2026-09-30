package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"axiombox/server"
)

func main() {
	port := flag.Int("port", 8080, "HTTP and WebSocket server port")
	concurrency := flag.Int("concurrency", 20, "Number of concurrent sandbox workers")
	queueCap := flag.Int("queue", 100, "Maximum queued requests before rejection")
	flag.Parse()

	log.Println("==================================================================")
	log.Println("           AXIOMBOX — KERNEL-ISOLATED RCE SANDBOX ENGINE          ")
	log.Println("==================================================================")
	log.Printf("Platform: %s/%s (Native Win32 Job Objects & Restricted Tokens)\n", runtime.GOOS, runtime.GOARCH)
	log.Printf("CPU Topology: %d logical cores available (Worker pool: %d)\n", runtime.NumCPU(), *concurrency)
	log.Printf("Queue Capacity: %d entries\n", *queueCap)
	log.Printf("Default Limits: 64MB RAM | 2.0s Deadline | 1 Process Max (Fork-Bomb Guard)\n")
	log.Println("==================================================================")

	srv := server.NewServer(*concurrency, *queueCap)
	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", *port),
		Handler:      srv.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	// Graceful shutdown handling
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("AxiomBox API Gateway listening on http://localhost:%d\n", *port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server listen failed: %v\n", err)
		}
	}()

	<-stopChan
	log.Println("\nShutdown signal received; gracefully draining sandboxes...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP shutdown error: %v\n", err)
	}

	log.Println("AxiomBox shutdown complete. All ephemeral workspaces purged.")
}
