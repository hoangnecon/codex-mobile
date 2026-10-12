package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"codex-server/internal/account"
	"codex-server/internal/archive"
	"codex-server/internal/api"
	"codex-server/internal/appserver"
	"codex-server/internal/auth"
	"codex-server/internal/events"
	"codex-server/internal/files"
	"codex-server/internal/git"
	"codex-server/internal/skills"
	"codex-server/internal/static"
	"codex-server/internal/telegram"
	"codex-server/internal/terminal"
	"codex-server/internal/tunnel"
)

func main() {
	portFlag := flag.Int("port", 18923, "Port to listen on")
	hostFlag := flag.String("host", "0.0.0.0", "Host/interface to bind on")
	codexPathFlag := flag.String("codex-path", "", "Path to codex binary (defaults to look in PATH)")
	staticDirFlag := flag.String("static-dir", "", "Path to static frontend dist directory")
	passwordFlag := flag.String("password", "", "Password for remote access (or CODEX_PASSWORD env)")
	tunnelFlag := flag.Bool("tunnel", false, "Start Cloudflare tunnel automatically")
	flag.Parse()

	port := *portFlag
	if envPort := os.Getenv("PORT"); envPort != "" {
		_, _ = fmt.Sscanf(envPort, "%d", &port)
	}

	host := *hostFlag
	if envHost := os.Getenv("HOST"); envHost != "" {
		host = envHost
	}

	password := *passwordFlag
	if envPwd := os.Getenv("CODEX_PASSWORD"); envPwd != "" && password == "" {
		password = envPwd
	}

	staticDir := *staticDirFlag
	if staticDir == "" {
		// Check common locations: ./dist, ../dist
		candidates := []string{"./dist", "../dist", "codex-mobile/dist"}
		for _, c := range candidates {
			if info, err := os.Stat(c); err == nil && info.IsDir() {
				staticDir = c
				break
			}
		}
	}
	if staticDir != "" {
		if abs, err := filepath.Abs(staticDir); err == nil {
			staticDir = abs
		}
	}

	log.Printf("[codex-server] Starting Golang backend...")
	log.Printf("[codex-server] Static assets directory: %s", staticDir)

	// 1. Initialize app-server Bridge
	bridgeCfg := appserver.Config{
		CodexPath: *codexPathFlag,
	}
	bridge := appserver.NewBridge(bridgeCfg)
	if err := bridge.Start(); err != nil {
		log.Printf("[codex-server] Warning: Failed to start codex app-server initially: %v (will retry on first request)", err)
	} else {
		log.Printf("[codex-server] Connected to codex app-server child process")
	}
	defer bridge.Close()

	// 2. Initialize Realtime Event Hub
	hub := events.NewHub()

	// 3. Initialize File Service
	filesSvc := files.NewService()

	// 4. Initialize Git and Skills Services
	gitSvc := git.NewService()
	skillsSvc := skills.NewService(bridge)
	archiveSvc := archive.NewService()
	termMgr := terminal.NewManager()
	accountSvc := account.NewService()
	authMgr := auth.NewManager(password)
	telegramBr := telegram.NewBridge(bridge)
	telegramBr.Start()
	defer telegramBr.Stop()

	// 5. Initialize API Router
	router := api.NewRouter(
		bridge,
		hub,
		filesSvc,
		gitSvc,
		skillsSvc,
		archiveSvc,
		termMgr,
		accountSvc,
		authMgr,
		telegramBr,
	)

	// 5. Attach SPA Handler for static web UI (uses embedded frontend assets by default)
	embeddedFS, err := static.GetEmbeddedFS()
	if err != nil {
		log.Printf("[codex-server] Warning: embedded filesystem error: %v", err)
	}
	spa := static.NewSPAServer(staticDir, embeddedFS)
	router.Mux().NotFound(spa.ServeHTTP)

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	server := &http.Server{
		Addr:         addr,
		Handler:      router.Mux(),
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
	}

	// Server listen in background
	go func() {
		log.Printf("[codex-server] Listening on http://%s (LAN and local access)", addr)
		log.Printf("[codex-server] Open in browser: http://localhost:%d", port)
		tunnel.PrintQRCode(fmt.Sprintf("http://localhost:%d", port))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[codex-server] HTTP server error: %v", err)
		}
	}()

	// Start Cloudflare tunnel if requested
	if *tunnelFlag {
		go func() {
			_, cleanup, err := tunnel.StartCloudflared(port)
			if err != nil {
				log.Printf("[codex-server] Tunnel error: %v", err)
				return
			}
			defer cleanup()
		}()
	}

	// Graceful shutdown handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Printf("[codex-server] Shutting down gracefully...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = server.Shutdown(shutdownCtx)
	_ = bridge.Close()
	log.Printf("[codex-server] Goodbye.")
}
