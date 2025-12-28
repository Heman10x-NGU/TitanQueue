package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/redis/go-redis/v9"
)

func main() {
	redisAddr := flag.String("redis", "localhost:6379", "Redis server address")
	port := flag.Int("port", 8080, "HTTP server port")
	flag.Parse()

	// Create Redis client
	client := redis.NewClient(&redis.Options{
		Addr: *redisAddr,
	})

	// Verify Redis connection
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis at %s: %v", *redisAddr, err)
	}
	log.Printf("Connected to Redis at %s", *redisAddr)

	// Create inspector and handler
	inspector := NewInspector(client)
	handler, err := NewHandler(inspector)
	if err != nil {
		log.Fatalf("Failed to create handler: %v", err)
	}

	// Setup routes
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// Start server
	addr := fmt.Sprintf(":%d", *port)
	server := &http.Server{Addr: addr, Handler: mux}

	// Handle shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		server.Close()
	}()

	log.Printf("TitanQueue Monitor starting on http://localhost%s", addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
