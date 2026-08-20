// Command server runs the real-time state synchronization engine. All
// wiring lives in internal/app so cmd/server and the integration tests
// build an identical stack; this file only owns the process lifecycle
// (listen, wait for a signal, shut down).
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"realtime-engine/internal/app"
	"realtime-engine/internal/config"
)

func main() {
	cfg := config.Load()
	log.Printf("starting realtime-engine node=%s addr=%s redis=%q", cfg.NodeID, cfg.HTTPAddr, cfg.RedisAddr)

	a := app.Build(cfg)
	defer a.Shutdown()

	httpServer := &http.Server{Addr: cfg.HTTPAddr, Handler: a.Mux}

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
}
