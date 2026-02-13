package main

import (
	"context"
	"embed"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ab0oo/gopds/internal/database"
	"github.com/ab0oo/gopds/internal/scanner"
	"github.com/ab0oo/gopds/internal/web"
)

//go:embed web/ui/*
var uiFS embed.FS

func main() {
	// 1. Configuration from Environment Variables (Docker Friendly)
	bookPath := os.Getenv("BOOK_PATH")
	if bookPath == "" {
		bookPath = "./books"
	}
	dbPath := "./data/gopds.db"

	// 2. Initialize Database
	db, err := database.New(dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	// 3. Start Scanner in the background
	s := scanner.New(db)
	go func() {
		if err := s.Start(bookPath); err != nil {
			log.Printf("Scanner error: %v", err)
		}
	}()

	// 4. Setup Web Server
	srv := web.NewServer(db, uiFS)
	httpServer := &http.Server{
		Addr:    ":8880",
		Handler: srv.Router(),
	}

	// 5. Graceful Shutdown Logic
	// Create a channel to listen for OS signals (SIGTERM, SIGINT)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Run the server in a goroutine so it doesn't block
	go func() {
		log.Printf("GoPDS is running on http://localhost:8880")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait here until we receive a signal
	<-stop
	log.Println("Shutting down GoPDS...")

	// Create a 5-second timeout for the shutdown process
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	log.Println("Exited cleanly.")
}
