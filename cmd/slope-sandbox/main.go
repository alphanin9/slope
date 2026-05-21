package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"slope/internal/api"
	"slope/internal/artifact"
	"slope/internal/config"
	"slope/internal/controller"
	"slope/internal/guest"
	"slope/internal/store"
	"slope/internal/worker"
)

func main() {
	configPath := flag.String("config", "config.example.yaml", "configuration file")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("config validation failed", "error", err)
		os.Exit(1)
	}
	db, err := store.Open(cfg.DatabasePath)
	if err != nil {
		log.Error("open database failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.UpsertMachines(context.Background(), cfg.Machines); err != nil {
		log.Error("seed machines failed", "error", err)
		os.Exit(1)
	}
	arts := artifact.New(cfg.StorageDir)
	vm, err := controller.NewLibvirtController(cfg.Backend.DSN)
	if err != nil {
		log.Error("libvirt controller failed", "error", err)
		os.Exit(1)
	}
	defer vm.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	w := &worker.Worker{
		Store:              db,
		Artifacts:          arts,
		VM:                 vm,
		Guest:              guest.NewHTTPRunner(),
		Log:                log,
		PollInterval:       time.Duration(cfg.SchedulerPollMS) * time.Millisecond,
		GuestPollInterval:  time.Duration(cfg.GuestPollMS) * time.Millisecond,
		ScreenshotInterval: time.Duration(cfg.Screenshots.IntervalSec) * time.Second,
		VMReadyTimeout:     time.Duration(cfg.VMReadyTimeoutMS) * time.Millisecond,
		GuestReadyTimeout:  time.Duration(cfg.GuestReadyTimeoutMS) * time.Millisecond,
		DedupeScreenshots:  cfg.Screenshots.Dedupe,
	}
	go w.Run(ctx)

	srv := &http.Server{
		Addr: cfg.HTTP.Addr,
		Handler: (&api.Server{
			Store:         db,
			Artifacts:     arts,
			Log:           log,
			MaxTimeout:    cfg.Limits.MaxTimeoutSec,
			MaxSampleSize: cfg.Limits.MaxSampleSize,
		}).Handler(),
	}
	go func() {
		log.Info("api listening", "addr", cfg.HTTP.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("http server failed", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
