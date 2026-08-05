package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/jack/umodel-sre-rca/internal/config"
	"github.com/jack/umodel-sre-rca/internal/feishu"
	"github.com/jack/umodel-sre-rca/internal/httpapi"
	"github.com/jack/umodel-sre-rca/internal/store"
)

const (
	feishuBaseURL            = "https://open.feishu.cn/open-apis"
	gatewayReadHeaderTimeout = 10 * time.Second
	gatewayReadTimeout       = 30 * time.Second
	gatewayWriteTimeout      = 30 * time.Second
	gatewayIdleTimeout       = 60 * time.Second
	gatewayShutdownTimeout   = 10 * time.Second
)

func main() {
	configPath := flag.String("config", "/etc/sre-rca/sre.yaml", "path to runtime configuration")
	flag.Parse()
	if err := run(*configPath); err != nil {
		log.Fatal(err)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load gateway config: %w", err)
	}
	if err := validateGatewayConfig(cfg); err != nil {
		return err
	}
	repo, err := store.Open(cfg.StateDB)
	if err != nil {
		return fmt.Errorf("open gateway state DB: %w", err)
	}
	defer repo.Close()
	notifier := newGatewayNotifier(cfg)
	server := newGatewayServer(cfg.ListenAddr, newGatewayHandler(cfg, repo, notifier))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("sre-gateway listening on %s", cfg.ListenAddr)
	return serveGateway(ctx, server)
}

func validateGatewayConfig(cfg config.Config) error {
	if cfg.CallbackToken == "" {
		return fmt.Errorf("callback_token is required")
	}
	if (cfg.Feishu.AppID == "") != (cfg.Feishu.AppSecret == "") {
		return fmt.Errorf("feishu app_id and app_secret must be configured together for recovery card updates")
	}
	return nil
}

func newGatewayNotifier(cfg config.Config) httpapi.RecoveryNotifier {
	if cfg.Feishu.AppID == "" {
		return nil
	}
	return feishu.NewClient(feishuBaseURL, cfg.Feishu.AppID, cfg.Feishu.AppSecret, cfg.Feishu.ChatID, nil)
}

func newGatewayHandler(cfg config.Config, repo *store.SQLiteRepository, notifier httpapi.RecoveryNotifier) http.Handler {
	return httpapi.NewGatewayWithNotifier(cfg.CallbackToken, cfg.CallbackCaptureDir, repo, notifier)
}

func newGatewayServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: gatewayReadHeaderTimeout,
		ReadTimeout:       gatewayReadTimeout,
		WriteTimeout:      gatewayWriteTimeout,
		IdleTimeout:       gatewayIdleTimeout,
	}
}

func serveGateway(ctx context.Context, server *http.Server) error {
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve gateway: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), gatewayShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown gateway: %w", err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve gateway after shutdown: %w", err)
	}
	return nil
}
