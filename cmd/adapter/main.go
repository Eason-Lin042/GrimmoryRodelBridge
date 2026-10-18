package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"grimmoryrodelbridge/internal/adapter"
	"grimmoryrodelbridge/internal/config"
)

func main() {
	key := flag.Bool("generate-key", false, "print a new encryption key and exit")
	health := flag.Bool("healthcheck", false, "check local readiness and exit")
	flag.Parse()
	if *key {
		var b [32]byte
		if _, e := rand.Read(b[:]); e != nil {
			os.Exit(1)
		}
		fmt.Println(base64.StdEncoding.EncodeToString(b[:]))
		return
	}
	if *health {
		listen := os.Getenv("LISTEN_ADDR")
		if listen == "" {
			listen = "127.0.0.1:8080"
		}
		client := http.Client{Timeout: 5 * time.Second}
		r, e := client.Get(adapter.HealthURL(listen))
		if e != nil {
			os.Exit(1)
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	c, e := config.FromEnv()
	if e != nil {
		log.Error("invalid configuration", "error", e.Error())
		os.Exit(1)
	}
	a, e := adapter.New(c, log)
	if e != nil {
		log.Error("cannot start adapter", "error", e.Error())
		os.Exit(1)
	}
	defer a.Close()
	server := &http.Server{Addr: c.Listen, Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 180 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10, ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelError)}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if e := server.Shutdown(shutdown); e != nil {
			server.Close()
		}
	}()
	log.Info("adapter listening", "address", c.Listen)
	if e = server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Error("HTTP server failed")
		os.Exit(1)
	}
}
