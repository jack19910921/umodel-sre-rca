package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jack/umodel-sre-rca/internal/cc"
	"github.com/jack/umodel-sre-rca/internal/config"
	"github.com/jack/umodel-sre-rca/internal/feishu"
	"github.com/jack/umodel-sre-rca/internal/runtime"
	"github.com/jack/umodel-sre-rca/internal/store"
	"github.com/jack/umodel-sre-rca/internal/worker"
)

const (
	defaultConfigPath = "/etc/sre-rca/sre.yaml"
	feishuBaseURL     = "https://open.feishu.cn/open-apis"
)

type runOneWorker interface {
	RunOne(context.Context) error
}

func main() {
	configPath := flag.String("config", defaultConfigPath, "path to runtime configuration")
	flag.Parse()
	if err := run(*configPath); err != nil {
		log.Fatal(err)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load worker config: %w", err)
	}
	if !cfg.Worker.Enabled {
		return fmt.Errorf("worker.enabled must be true to run sre-worker")
	}
	if err := validateWorkerConfig(cfg); err != nil {
		return err
	}
	repo, err := store.Open(cfg.StateDB)
	if err != nil {
		return fmt.Errorf("open worker state DB: %w", err)
	}
	defer repo.Close()

	evidence, err := runtime.NewEvidenceService(cfg, repo)
	if err != nil {
		return fmt.Errorf("compose evidence service: %w", err)
	}
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("read worker hostname: %w", err)
	}
	cards := feishu.NewClient(feishuBaseURL, cfg.Feishu.AppID, cfg.Feishu.AppSecret, cfg.Feishu.ChatID, nil)
	runner := cc.Runner{Timeout: time.Duration(cfg.Worker.TimeoutSeconds) * time.Second, MaxTurns: cfg.Worker.MaxTurns}
	configuredWorker := worker.New(repo, cards, evidence, runner, "sre-worker-"+hostname, time.Now)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("sre-worker started: poll_seconds=%d timeout_seconds=%d", cfg.Worker.PollSeconds, cfg.Worker.TimeoutSeconds)
	runLoop(ctx, configuredWorker, time.Duration(cfg.Worker.PollSeconds)*time.Second, time.Duration(cfg.Worker.TimeoutSeconds)*time.Second, nil, log.Printf)
	log.Print("sre-worker stopped")
	return nil
}

func validateWorkerConfig(cfg config.Config) error {
	if cfg.Feishu.AppID == "" || cfg.Feishu.AppSecret == "" || cfg.Feishu.ChatID == "" {
		return fmt.Errorf("worker.enabled requires feishu app_id, app_secret, and chat_id")
	}
	return nil
}

// runLoop owns polling only. The Gateway stays ingress-only; every worker call
// gets a fresh timeout so one stalled provider or RCA subprocess cannot leak a
// cancelled context into a later job.
func runLoop(ctx context.Context, worker runOneWorker, pollInterval, timeout time.Duration, ticks <-chan time.Time, logf func(string, ...any)) {
	if pollInterval <= 0 {
		pollInterval = 15 * time.Second
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if ticks == nil {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		ticks = ticker.C
	}

	runOnce(ctx, worker, timeout, logf)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			runOnce(ctx, worker, timeout, logf)
		}
	}
}

func runOnce(parent context.Context, worker runOneWorker, timeout time.Duration, logf func(string, ...any)) {
	callCtx, cancel := context.WithTimeout(parent, timeout)
	err := worker.RunOne(callCtx)
	cancel()
	if err == nil || errors.Is(err, store.ErrIncidentInactive) {
		return
	}
	logf("sre-worker RunOne: %v", err)
}
