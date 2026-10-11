// Command hqd runs the hq server.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gronnbeck/hq/internal/server"
	"github.com/gronnbeck/hq/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	dbPath := flag.String("db", "hq.db", "path to the SQLite database")
	openSignup := flag.Bool("open-signup", false, "let anyone create an account (for local development)")
	flag.Parse()

	// The admin token comes from the environment so it stays out of `ps`.
	adminToken := os.Getenv("HQ_ADMIN_TOKEN")
	if adminToken == "" && !*openSignup {
		log.Print("warning: HQ_ADMIN_TOKEN is unset and signup is closed, so no users can be created")
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.New(st, server.Options{AdminToken: adminToken, OpenSignup: *openSignup}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()

	log.Printf("hqd listening on %s", *addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
