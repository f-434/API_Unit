package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"apiunit/internal/api"
	"apiunit/internal/config"
	"apiunit/internal/engine"
	"apiunit/internal/model"
	"apiunit/internal/modules/injection"
	"apiunit/internal/modules/stress"
	"apiunit/internal/modules/tokenaudit"
	"apiunit/internal/protocol"
	"apiunit/internal/requestdiff"
	"apiunit/internal/secret"
	"apiunit/internal/seed"
	"apiunit/internal/store"
	"apiunit/internal/tokens"
)

func main() {
	cfg := config.Load()
	if err := cfg.EnsureDirs(); err != nil {
		log.Fatalf("create data dir: %v", err)
	}
	box, err := secret.LoadOrCreate(cfg.SecretPath())
	if err != nil {
		log.Fatalf("load secret: %v", err)
	}
	st, err := store.Open(cfg.DBPath(), box)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()
	if err := st.MarkInterrupted(); err != nil {
		log.Printf("mark interrupted runs: %v", err)
	}
	if err := st.EnsureBuiltinPrompts(seed.Prompts()); err != nil {
		log.Printf("seed prompts: %v", err)
	}
	if err := st.EnsureBuiltinAttacks(seed.Attacks()); err != nil {
		log.Printf("seed attacks: %v", err)
	}

	client := protocol.NewClient()
	counter := tokens.New(cfg.TokenizerDir, cfg.Offline)
	manager := engine.NewManager(st, client, counter, cfg.MaxConcurrentRuns)
	manager.Register(stress.New(st))
	manager.Register(tokenaudit.New(st))
	manager.Register(requestdiff.New(st))
	manager.Register(injection.New(st, client))

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.New(cfg, st, manager, counter, client, box).Router(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go purgeLoop(st, cfg.RetentionDays)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("API Unit %s listening on http://%s (data: %s, auth: %v)", cfg.Version, displayAddr(cfg.Addr), cfg.DataDir, cfg.AuthEnabled())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-stop
	log.Println("shutting down...")
	manager.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Println("bye")
}

func purgeLoop(st *store.Store, days int) {
	if days <= 0 {
		return
	}
	purge := func() {
		cutoff := model.NowMs() - int64(days)*24*3600*1000
		if n, err := st.PurgeBefore(cutoff); err != nil {
			log.Printf("purge old runs: %v", err)
		} else if n > 0 {
			log.Printf("purged %d old runs", n)
		}
	}
	purge()
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for range t.C {
		purge()
	}
}

func displayAddr(addr string) string {
	switch {
	case len(addr) > 0 && addr[0] == ':':
		return "127.0.0.1" + addr
	case len(addr) >= 7 && addr[:7] == "0.0.0.0":
		return "127.0.0.1" + addr[7:]
	default:
		return addr
	}
}
