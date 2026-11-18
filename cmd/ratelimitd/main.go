// Command ratelimitd exposes the ratelimitd middleware in front of a demo
// upstream and serves bucket state on a separate admin listener.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/s8xazw5fdp/ratelimitd"
)

type config struct {
	addr      string
	adminAddr string
	rate      float64
	burst     float64
	sweep     time.Duration
	idle      time.Duration
}

func main() {
	cfg := config{
		addr:      env("RL_ADDR", ":8080"),
		adminAddr: env("RL_ADMIN_ADDR", "127.0.0.1:8081"),
	}
	flag.StringVar(&cfg.addr, "addr", cfg.addr, "listen address for limited traffic")
	flag.StringVar(&cfg.adminAddr, "admin-addr", cfg.adminAddr, "listen address for admin endpoints")
	flag.Float64Var(&cfg.rate, "rate", envFloat("RL_RATE", 5), "sustained requests per second per tenant")
	flag.Float64Var(&cfg.burst, "burst", envFloat("RL_BURST", 10), "burst capacity per tenant")
	flag.DurationVar(&cfg.sweep, "sweep", time.Minute, "interval between idle bucket sweeps")
	flag.DurationVar(&cfg.idle, "idle", ratelimitd.DefaultIdleEviction, "idle time before a bucket is dropped")
	flag.Parse()

	if err := run(cfg); err != nil {
		slog.Error("shutdown", "err", err)
		os.Exit(1)
	}
}

func run(cfg config) error {
	limiter, err := ratelimitd.New(ratelimitd.Params{Rate: cfg.rate, Burst: cfg.burst})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		limiter.SweepEvery(cfg.sweep, cfg.idle, ctx.Done())
	}()

	edge := &http.Server{
		Addr:              cfg.addr,
		Handler:           limiter.Middleware(ratelimitd.DefaultKey)(http.HandlerFunc(demoUpstream)),
		ReadHeaderTimeout: 5 * time.Second,
	}
	admin := &http.Server{
		Addr:              cfg.adminAddr,
		Handler:           adminMux(limiter),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errs := make(chan error, 2)
	for _, srv := range []*http.Server{edge, admin} {
		go func(s *http.Server) {
			slog.Info("listening", "addr", s.Addr)
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs <- err
			}
		}(srv)
	}

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	slog.Info("draining")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, srv := range []*http.Server{edge, admin} {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
	}
	<-done
	return nil
}

// demoUpstream stands in for the real handler: it echoes the tenant so the
// limiter can be exercised with curl.
func demoUpstream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"tenant": r.Header.Get(ratelimitd.TenantHeader),
		"path":   r.URL.Path,
	})
}

func adminMux(l *ratelimitd.Limiter) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "ok buckets=%d\n", l.Len())
	})
	mux.HandleFunc("GET /admin/buckets", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(l.Snapshot())
	})
	return mux
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}
