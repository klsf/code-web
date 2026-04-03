package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	passwordFlag := flag.String("password", "codex", "login password for Code Web")
	flag.Parse()

	availableProviders = detectAvailableProviders()
	activeProvider = selectDefaultProvider(availableProviders)
	if _, ok := availableProviders[activeProvider.ID()]; !ok {
		log.Fatalf("%s is not available on this machine", activeProvider.DisplayName())
	}

	workdir, err := os.Getwd()
	if err != nil {
		log.Fatalf("detect app workdir: %v", err)
	}
	defaultWorkdir = filepath.Clean(workdir)

	if err := os.MkdirAll(uploadsDir, 0o755); err != nil {
		log.Fatalf("create upload dir: %v", err)
	}
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		log.Fatalf("create log dir: %v", err)
	}
	if err := ensureProviderAvailable(); err != nil {
		if activeProvider.RequiresAuth() {
			log.Fatalf("%v; also make sure `%s login` has been completed on this machine", err, activeProvider.Executable())
		}
		log.Fatal(err)
	}

	store := &sessionStore{
		sessions:      make(map[string]*sessionRuntime),
		auth:          newCodexAuthManager(),
		accountStatus: make(map[string]cachedAccountStatus),
		meta: appMeta{
			Provider:       activeProvider.ID(),
			Model:          detectCodexModel(),
			Cwd:            defaultWorkdir,
			ApprovalPolicy: "never",
			ServiceTier:    detectServiceTier(),
		},
		authToken: authTokenForPassword(*passwordFlag),
	}
	store.meta.Model = defaultModelForProvider()
	store.meta.FastMode = strings.EqualFold(store.meta.ServiceTier, "fast")
	store.maxConcurrent = detectTaskConcurrency()
	store.taskSlots = make(chan struct{}, store.maxConcurrent)

	if err := activeProvider.Start(store); err != nil {
		log.Fatal(err)
	}
	defer activeProvider.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/", store.handleIndex)
	mux.Handle("/app/", staticAssetsHandler("static"))
	mux.Handle("/style.css", staticAssetsHandler("static"))
	mux.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(uploadsDir))))
	mux.HandleFunc("/app-config.js", store.handleAppConfig)
	mux.HandleFunc("/ws", store.handleWS)
	mux.HandleFunc("/api/login", store.handleLogin)
	mux.HandleFunc("/api/auth", store.handleAuth)
	mux.HandleFunc("/api/logout", store.handleLogout)
	mux.HandleFunc("/api/session/new", store.handleNewSession)
	mux.HandleFunc("/api/session/restore", store.handleRestoreSession)
	mux.HandleFunc("/api/send", store.handleSend)
	mux.HandleFunc("/api/command", store.handleCommand)
	mux.HandleFunc("/api/status", store.handleStatus)
	mux.HandleFunc("/api/models", store.handleModels)
	mux.HandleFunc("/api/skills", store.handleSkills)
	mux.HandleFunc("/api/sessions", store.handleSessions)
	if _, ok := availableProviders[providerCodex]; ok {
		mux.HandleFunc("/codex-auth", store.handleCodexAuthPage)
		mux.HandleFunc("/auth/callback", store.handleCodexAuthCallback)
		mux.HandleFunc("/api/codex-auth/status", store.handleCodexAuthStatus)
		mux.HandleFunc("/api/codex-auth/start", store.handleCodexAuthStart)
		mux.HandleFunc("/api/codex-auth/complete", store.handleCodexAuthComplete)
	}

	server := &http.Server{
		Addr:    addr,
		Handler: store.withAuth(mux),
	}
	serverErr := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	shutdownSignals := []os.Signal{os.Interrupt, syscall.SIGTERM}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals...)
	defer stop()

	appLog.Info().Str("addr", addr).Msg("server listening")
	appLog.Info().
		Str("provider", activeProvider.ID()).
		Str("executable", activeProvider.Executable()).
		Msg("provider info")
	appLog.Info().Int("concurrency", store.maxConcurrent).Msg("task concurrency limit")
	select {
	case err := <-serverErr:
		if err == nil {
			return
		}
		log.Fatal(err)
	case <-ctx.Done():
	}

	appLog.Warn().Msg("shutdown signal received, stopping services")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		appLog.Warn().Err(err).Msg("http shutdown failed")
		if closeErr := server.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
			appLog.Error().Err(closeErr).Msg("force close http server failed")
		}
	}
}
