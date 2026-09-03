// Command server is the 8Ball game server entrypoint: Gin for the plain
// HTTP surface (health/info/rooms/metrics) plus the WebSocket gateway at /ws.
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

	"github.com/gin-gonic/gin"

	"github.com/yourgame/8ball-backend/pkg/gateway"
	"github.com/yourgame/8ball-backend/pkg/monitor"
	"github.com/yourgame/8ball-backend/pkg/protocol"
	"github.com/yourgame/8ball-backend/pkg/room"
	"github.com/yourgame/8ball-backend/pkg/transport"
)

const version = "0.1.0"

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	metrics := monitor.New()
	hub := transport.NewHub()
	rooms := room.NewManager(room.DefaultOptions(), metrics)
	gw := gateway.New(gateway.DefaultConfig(), hub, rooms, metrics)

	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Legacy smoke-test endpoint, kept for backwards compatibility.
	router.GET("/hello", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "Hello, 8Ball Backend!",
			"version": version,
			"author":  "Go Backend Team",
		})
	})

	router.GET("/info", func(c *gin.Context) {
		snapshot := metrics.Snapshot()
		c.JSON(http.StatusOK, gin.H{
			"name":            "8Ball Backend Server",
			"version":         version,
			"description":     "Real-time online 8-ball pool game backend",
			"protocolVersion": protocol.ProtocolVersion,
			"status":          "running",
			"transport":       "websocket",
			"websocketPath":   "/ws?playerId=&name=&token=",
			"connections":     snapshot["server.connection_count"],
			"rooms":           snapshot["game.active_rooms"],
			"matchQueueSize":  snapshot["game.match_queue_size"],
		})
	})

	router.GET("/metrics", func(c *gin.Context) {
		c.JSON(http.StatusOK, metrics.Snapshot())
	})

	router.GET("/rooms", func(c *gin.Context) {
		sums := rooms.Summaries()
		if sums == nil {
			sums = []room.Summary{}
		}
		c.JSON(http.StatusOK, gin.H{
			"count":  len(sums),
			"rooms":  sums,
			"queued": rooms.QueueSize(),
		})
	})

	// The main event: the game WebSocket.
	router.GET("/ws", func(c *gin.Context) {
		gw.HandleWS(c.Writer, c.Request)
	})

	// Unity WebGL static files (index.html + /Build/* + /TemplateData/*).
	registerWebGL(router)

	srv := &http.Server{
		Addr:    "0.0.0.0:8080",
		Handler: router,
		// ReadHeaderTimeout bounds only the request-header phase, so it defends
		// the plain HTTP endpoints against slowloris-style stalls without ever
		// touching the long-lived WebSocket connections (those are hijacked
		// after the upgrade and manage their own deadlines).
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Println("========================================")
		log.Println("8Ball Backend Server", version)
		log.Println("protocol", protocol.ProtocolVersion)
		log.Println("========================================")
		log.Printf("listening on http://localhost:%s", port)
		log.Println("  GET /healthz  - health check")
		log.Println("  GET /info     - server info")
		log.Println("  GET /metrics  - runtime counters")
		log.Println("  GET /rooms    - live room list (debug)")
		log.Println("  GET /ws       - game WebSocket")
		log.Println("========================================")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	// Graceful shutdown: stop accepting, close sockets, close rooms.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	rooms.Shutdown("server_shutdown")
	hub.CloseAll("server_shutdown")
	log.Println("bye")
}
