package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aria-tec/aegis-runtime/pkg/api"
	"github.com/aria-tec/aegis-runtime/pkg/llm"
	"github.com/aria-tec/aegis-runtime/pkg/orchestrator"
	"github.com/aria-tec/aegis-runtime/pkg/sandbox"
	"github.com/aria-tec/aegis-runtime/pkg/storage"
)

// sanitizeLog strips CR/LF characters to prevent log injection (G706/CWE-117).
func sanitizeLog(s string) string {
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

func main() {
	port := sanitizeLog(os.Getenv("AEGIS_PORT"))
	if port == "" {
		port = "8085"
	}

	// 1. Initialize Storage Layer (PostgreSQL or SQLite)
	var store storage.Store
	var err error

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL != "" {
		log.Println("Initializing PostgreSQL storage backend...")
		store, err = storage.NewPostgresStore(databaseURL)
		if err != nil {
			log.Fatalf("Failed to initialize PostgreSQL storage: %v", err)
		}
	} else {
		dbPath := filepath.Clean(os.Getenv("AEGIS_DB_PATH"))
		if dbPath == "" || dbPath == "." {
			dbPath = "data/aegis.db"
		}
		if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
			_ = os.MkdirAll(dir, 0750) // #nosec G703 -- bounded directory creation
		}
		log.Printf("Initializing SQLite storage backend at %s...\n", sanitizeLog(dbPath)) // #nosec G706 -- sanitized log
		store, err = storage.NewSQLiteStore(dbPath)
		if err != nil {
			log.Fatalf("Failed to initialize SQLite storage: %v", err)
		}
	}
	defer func() {
		_ = store.Close()
	}()

	// 2. Initialize LLM Provider Driver
	var driver llm.Driver
	llmProvider := strings.ToLower(os.Getenv("LLM_PROVIDER"))
	openaiKey := os.Getenv("OPENAI_API_KEY")
	geminiKey := os.Getenv("GEMINI_API_KEY")
	baseURL := os.Getenv("LLM_BASE_URL")
	model := os.Getenv("LLM_MODEL")

	if llmProvider == "openai" || llmProvider == "gemini" || openaiKey != "" || geminiKey != "" {
		apiKey := openaiKey
		if apiKey == "" {
			apiKey = geminiKey
		}
		driver = llm.NewOpenAICompatibleDriver(baseURL, apiKey, model)
		log.Printf("Initialized OpenAI-Compatible LLM Driver (Model: %s, BaseURL: %s)\n", sanitizeLog(driver.(*llm.OpenAICompatibleDriver).Model()), sanitizeLog(driver.(*llm.OpenAICompatibleDriver).BaseURL())) // #nosec G706
	} else {
		mock := llm.NewMockDriver()
		driver = mock
		log.Println("Initialized Deterministic Mock LLM Driver (Default)")
	}

	// 3. Initialize Sandboxed Runner & Orchestrator Engine
	scratchDir := filepath.Clean(os.Getenv("AEGIS_SCRATCH_DIR"))
	if scratchDir == "" || scratchDir == "." {
		scratchDir = "scratch"
	}
	_ = os.MkdirAll(scratchDir, 0750) // #nosec G703 -- bounded directory creation

	runner := sandbox.NewProcessRunner(scratchDir)
	engine := orchestrator.NewEngine(store, driver, runner)
	server := api.NewServer(engine, store)

	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second, // G112: Mitigate Slowloris attacks
	}

	// 4. Start HTTP Daemon
	go func() {
		log.Printf("🛡️ Aegis-Runtime Server running on :%s\n", sanitizeLog(port)) // #nosec G706
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failure: %v", err)
		}
	}()

	// 5. Graceful Shutdown on SIGINT/SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down Aegis-Runtime server gracefully (5s timeout)...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("Server forced shutdown error: %v\n", err)
	}
	log.Println("Aegis-Runtime stopped.")
}
