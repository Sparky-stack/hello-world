// Command hello-world is a tiny HTTP service used to exercise the koorikla
// platform (OpenChoreo on Cluster API clusters) end to end.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
)

// version is overridden at build time: -ldflags "-X main.version=<git sha>".
var version = "dev"

const (
	shutdownTimeout = 10 * time.Second
	dbTimeout       = 3 * time.Second
)

// Greeting is the JSON body returned by GET /.
type Greeting struct {
	Message  string `json:"message"`
	Hostname string `json:"hostname"`
	Version  string `json:"version"`
}

// Config is the runtime configuration, read from the environment.
type Config struct {
	Port     string
	Greeting string
	Hostname string
	Version  string
	// Database is set when the platform wired a database: DATABASE_URL, or libpq's PGHOST (+ PGPORT, PGUSER, ...).
	Database bool
}

func configFromEnv(getenv func(string) string) Config {
	cfg := Config{
		Port:     getenv("PORT"),
		Greeting: getenv("GREETING"),
		Version:  version,
	}
	cfg.Database = getenv("DATABASE_URL") != "" || getenv("PGHOST") != ""
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.Greeting == "" {
		cfg.Greeting = "hello"
	}
	if h, err := os.Hostname(); err == nil {
		cfg.Hostname = h
	}
	return cfg
}

// pingDB opens one connection with DATABASE_URL (pgx falls back to the PG* variables when it is empty) and runs
// SELECT 1. One connection per request keeps it simple: GET /db is a demo probe, not a hot path.
func pingDB(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	var one int
	return conn.QueryRow(ctx, "SELECT 1").Scan(&one)
}

// newHandler serves the API. ping checks the database for GET /db; it is never called when cfg.Database is false.
func newHandler(cfg Config, ping func(context.Context) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, Greeting{
			Message:  cfg.Greeting,
			Hostname: cfg.Hostname,
			Version:  cfg.Version,
		})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": cfg.Version})
	})
	// Not part of /healthz on purpose: a database outage must not restart or unroute the app.
	mux.HandleFunc("GET /db", func(w http.ResponseWriter, r *http.Request) {
		if !cfg.Database {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not configured"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), dbTimeout)
		defer cancel()
		if err := ping(ctx); err != nil {
			// The cause names host, user and database: log it for operators, keep it out of the public response.
			slog.Warn("database check failed", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "error"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// serve runs the HTTP server on ln until ctx is cancelled, then shuts down
// gracefully, letting in-flight requests finish within shutdownTimeout.
func serve(ctx context.Context, ln net.Listener, h http.Handler, log *slog.Logger) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", ln.Addr().String())
		errCh <- srv.Serve(ln)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
	cfg := configFromEnv(os.Getenv)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", ":"+cfg.Port)
	if err != nil {
		log.Error("listen failed", "err", err)
		os.Exit(1)
	}
	log.Info("starting", "version", cfg.Version, "greeting", cfg.Greeting, "database", cfg.Database)
	if err := serve(ctx, ln, newHandler(cfg, pingDB), log); err != nil {
		log.Error("server error", "err", err)
		os.Exit(1)
	}
	log.Info("stopped")
}
