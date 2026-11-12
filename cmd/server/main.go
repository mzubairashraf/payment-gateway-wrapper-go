// Command server runs the payment gateway wrapper: one HTTP API for charges
// and refunds in front of interchangeable payment gateways.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mzubairashraf/payment-gateway-wrapper-go/internal/config"
	"github.com/mzubairashraf/payment-gateway-wrapper-go/internal/gateway/authorizenet"
	"github.com/mzubairashraf/payment-gateway-wrapper-go/internal/gateway/simulator"
	"github.com/mzubairashraf/payment-gateway-wrapper-go/internal/httpapi"
	"github.com/mzubairashraf/payment-gateway-wrapper-go/internal/payment"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "server:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	var gateways []payment.Gateway
	if cfg.AuthorizeNet {
		gateways = append(gateways, authorizenet.New(authorizenet.Config{
			APILoginID: cfg.AuthNetLoginID, TransactionKey: cfg.AuthNetKey,
			Endpoint: cfg.AuthNetEndpoint, Currency: cfg.AuthNetCurrency, Timeout: cfg.GatewayTimeout,
		}))
	}
	if cfg.Simulator {
		gateways = append(gateways, simulator.New())
	}
	registry := payment.NewRegistry(gateways...)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(httpapi.Options{Gateways: registry, APIToken: cfg.APIToken, Logger: logger}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// Longer than the gateway timeout, so a slow gateway produces a
		// classified 504 from the handler instead of a dropped connection.
		WriteTimeout: cfg.GatewayTimeout + 10*time.Second,
		IdleTimeout:  60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	logger.Info("listening", slog.String("addr", cfg.Addr), slog.Any("gateways", registry.Names()))

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	// Let in-flight charges finish: cutting one off mid-request would leave
	// its outcome unknown to the caller.
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGracePeriod)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
