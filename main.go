package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Config struct {
	Sources               []Source `json:"sources"`
	Cron                  string   `json:"cron"`
	Timezone              string   `json:"timezone"`
	RateLimitMilliseconds int      `json:"rate_limit_milliseconds"`
	MaxPages              int      `json:"max_pages"`
}

func ReadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return config, err
	}
	if len(config.Sources) == 0 {
		return config, fmt.Errorf("no sources configured")
	}
	seen := map[string]bool{}
	for _, s := range config.Sources {
		if s.ID == "" || s.Market == "" || s.URL == "" || s.Type == "" {
			return config, fmt.Errorf("source must have id, market, type and url")
		}
		if seen[s.ID] {
			return config, fmt.Errorf("duplicate source id %q", s.ID)
		}
		seen[s.ID] = true
		if strings.ContainsAny(s.ID, " /\\") {
			return config, fmt.Errorf("invalid id %q", s.ID)
		}
	}
	if config.Cron == "" {
		config.Cron = "0 3 * * *"
	}
	if config.Timezone == "" {
		config.Timezone = "Europe/Skopje"
	}
	if config.RateLimitMilliseconds <= 0 {
		config.RateLimitMilliseconds = 1500
	}
	if config.MaxPages <= 0 {
		config.MaxPages = 500
	}
	return config, nil
}
func main() {
	var configFile, dbPath, addr string
	var once, noStartup bool
	flag.StringVar(&configFile, "config", "sources.json", "source configuration JSON")
	flag.StringVar(&dbPath, "db", "data/groceries.sqlite", "SQLite database location")
	flag.StringVar(&addr, "listen", ":8080", "HTTP listen address")
	flag.BoolVar(&once, "once", false, "run a single sync and exit")
	flag.BoolVar(&noStartup, "no-startup", false, "do not sync immediately on server start")
	flag.Parse()
	cfg, err := ReadConfig(configFile)
	if err != nil {
		log.Fatal(err)
	}
	cronSpec := os.Getenv("SYNC_CRON")
	if cronSpec == "" {
		cronSpec = cfg.Cron
	}
	schedule, err := ParseCron(cronSpec)
	if err != nil {
		log.Fatal(err)
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0750); err != nil {
		log.Fatal(err)
	}
	db, err := OpenDB(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		log.Fatal(err)
	}
	collector := &Collector{DB: db, Fetcher: NewFetcher(time.Duration(cfg.RateLimitMilliseconds) * time.Millisecond), MaxPages: cfg.MaxPages, BrowserURL: os.Getenv("BROWSER_BRIDGE_URL")}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if once {
		if err := collector.SyncAll(ctx, cfg.Sources); err != nil {
			log.Fatal("sync finished with errors: ", err)
		}
		log.Print("sync completed")
		return
	}
	api := &API{DB: db, Collector: collector, Sources: cfg.Sources, AdminToken: os.Getenv("ADMIN_TOKEN")}
	server := &http.Server{Addr: addr, Handler: api.Routes(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 50 * time.Minute, IdleTimeout: 90 * time.Second}
	go func() {
		if !noStartup {
			log.Print("initial sync starting")
			if err := collector.SyncAll(ctx, cfg.Sources); err != nil {
				log.Printf("initial sync partial or failed: %v", err)
			}
		}
		for ctx.Err() == nil {
			next, err := schedule.Next(time.Now(), loc)
			if err != nil {
				log.Printf("cron schedule error: %v", err)
				return
			}
			log.Printf("next catalog sync: %s", next.In(loc).Format(time.RFC3339))
			timer := time.NewTimer(time.Until(next))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if err := collector.SyncAll(ctx, cfg.Sources); err != nil {
				log.Printf("scheduled sync partial or failed: %v", err)
			}
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("API listening on %s", addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
