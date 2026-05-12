package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"rqstdev/api/internal/config"
	"rqstdev/api/internal/server"
	"rqstdev/api/internal/store"
)

func main() {
	logger := log.New(os.Stdout, "rqstdev-api ", log.LstdFlags|log.Lmsgprefix)

	cfgPath := config.FlagPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Fatalf("load config: %v", err)
	}

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		logger.Fatalf("create data dir: %v", err)
	}
	if err := os.MkdirAll(cfg.VMsDir, 0o755); err != nil {
		logger.Fatalf("create vms dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		logger.Fatalf("create db dir: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		logger.Fatalf("open store: %v", err)
	}
	defer st.Close()

	initCtx, initCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := st.EnsureDefaultTemplate(initCtx, cfg.DefaultTemplateName, cfg.DefaultTemplateImagePath); err != nil {
		initCancel()
		logger.Fatalf("seed default template: %v", err)
	}
	initCancel()

	srv := server.New(cfg, logger, st)

	go func() {
		logger.Printf("listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("serve: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Fatalf("shutdown: %v", err)
	}
}
