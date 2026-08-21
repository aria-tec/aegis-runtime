# 🛡️ Software Design Specification: Aegis-Runtime v1.0.0

**Document ID:** `SPEC-AEGIS-2026-08-21`  
**Date:** 2026-08-21  
**Status:** Approved by LLM Council (100% Unanimous)  
**Target Repository:** `ExProject/aegis-runtime`  
**Language/Runtime:** Go 1.27 (Zero CGO) + Docker Engine API  

---

## 1. Executive Summary & Vision

**Aegis-Runtime** adalah *Standalone Polyglot AI Agent Execution Engine & Sandboxed Tool Gateway* independen. Sistem ini dirancang untuk menyelesaikan problem mendasar pada deployment AI Agents otonom di ranah produksi:
1. **Durable Workflow Execution & Deterministic Replay:** Mengeliminasi pemborosan ribuan dolar token dan kehilangan state saat sistem mati mendadak di tengah multi-step reasoning.
2. **Hardened Tool Sandboxing:** Mengisolasi eksekusi kode (Python/Bash) dan pemanggilan tool dari sistem server utama dengan pembatasan kuota dan *environment variable scrubbing*.
3. **Universal Interoperability (MCP & REST):** Menyediakan antarmuka standar (Model Context Protocol & REST JSON API) yang dapat dikonsumsi oleh aplikasi apa pun (.NET Microsoft eShop, Go Mini-Svix, Node.js frontend, Python CLI) tanpa *vendor lock-in*.

---

## 2. Core Architectural Pillars

```
 [ Downstream Clients ]                 [ Aegis-Runtime Go Daemon (:8085) ]
 ┌─────────────────────┐                ┌────────────────────────────────────────────────┐
 │ • eShop (.NET 9)    │                │ 🌐 pkg/api (Ingress Gateway)                   │
 │ • Mini-Svix (Go)    │ ──REST / MCP──► │ • POST /api/v1/agents/execute                  │
 │ • Future SaaS (Node)│                │ • GET  /api/v1/workflows/{id}                  │
 │ • CLI / Python      │                │ • GET  /api/v1/workflows/{id}/history          │
 └─────────────────────┘                └───────────────────────┬────────────────────────┘
                                                                │
                                        ┌───────────────────────▼────────────────────────┐
                                        │ ⚙️ pkg/orchestrator (Durable State Machine)     │
                                        │ • Step Checkpoints & Event-Sourced History     │
                                        │ • Deterministic Crash-Recovery Replay          │
                                        │ • Step Limits (max 10) & Token Budget Guards   │
                                        └───────────┬──────────────────────┬─────────────┘
                                                    │                      │
                       ┌────────────────────────────┘                      └────────────────────────────┐
                       ▼                                                                                ▼
        ┌─────────────────────────────┐                                                  ┌─────────────────────────────┐
        │ 🧠 pkg/llm (AI Drivers)     │                                                  │ 📦 pkg/sandbox (Tool Runner)│
        ├─────────────────────────────┤                                                  ├─────────────────────────────┤
        │ • MockDriver (100% offline) │                                                  │ • ProcessRunner (1-5ms fast)│
        │ • OpenAICompatibleDriver    │                                                  │ • DockerRunner (Container)  │
        │   (Gemini, OpenAI, Ollama)  │                                                  │ • Env Variable Scrubbing    │
        └─────────────────────────────┘                                                  └─────────────────────────────┘
                                                    │
                                        ┌───────────▼────────────────────────────┐
                                        │ 💾 pkg/storage (Event Sourcing Store)  │
                                        ├────────────────────────────────────────┤
                                        │ • SQLiteStore (Pure Go, default local) │
                                        │ • PostgresStore (Production adapter)   │
                                        │ • Auto-embedded migrations (go:embed)  │
                                        └────────────────────────────────────────┘
```

---

## 3. Detailed Component Specifications

### 3.1. Package `pkg/storage` (State & Event Sourcing Store)
- **Interface Definition:**
  ```go
  type Store interface {
      CreateWorkflow(ctx context.Context, wf *domain.Workflow) error
      UpdateWorkflow(ctx context.Context, wf *domain.Workflow) error
      GetWorkflow(ctx context.Context, id string) (*domain.Workflow, error)
      AppendEvent(ctx context.Context, evt *domain.WorkflowEvent) error
      GetEvents(ctx context.Context, workflowID string) ([]domain.WorkflowEvent, error)
      Close() error
  }
  ```
- **Driver Implementations:**
  - `SQLiteStore`: Menggunakan `modernc.org/sqlite` (pure Go, CGO-free). Default database file: `data/aegis.db`. Mode WAL aktif (`PRAGMA journal_mode = WAL;`).
  - `PostgresStore`: Menggunakan standard SQL driver (`github.com/jackc/pgx/v5/stdlib` atau `lib/pq`).
- **Migration Strategy:** SQL schema embedded via `//go:embed migrations/*.sql` dan otomatis dijalankan saat startup.

### 3.2. Package `pkg/llm` (AI Provider Drivers)
- **Interface Definition:**
  ```go
  type Driver interface {
      GenerateStep(ctx context.Context, req domain.StepPromptRequest) (*domain.StepPromptResponse, error)
  }
  ```
- **Implementations:**
  - `MockDriver`: Menjalankan skrip step deterministik yang terprogram (misal: Step 1 $\rightarrow$ ToolCall `calculate_tax`, Step 2 $\rightarrow$ Final Output). Wajib untuk pengujian offline dan verifikasi crash-recovery.
  - `OpenAICompatibleDriver`: Universal HTTP JSON client yang mendukung Google Gemini (OpenAI endpoint), OpenAI, DeepSeek, Groq, dan Ollama lokal.

### 3.3. Package `pkg/sandbox` (Tool Execution Isolation)
- **Interface Definition:**
  ```go
  type Runner interface {
      Execute(ctx context.Context, req domain.ToolExecutionRequest) (*domain.ToolExecutionResult, error)
  }
  ```
- **Implementations:**
  - `ProcessRunner` (Default Fast): Menggunakan `os/exec.CommandContext`. Menghapus semua environment variables host (`cmd.Env = []string{}`) dan hanya menyalurkan variabel yang didefinisikan aman. Mengisolasi direktori ke `scratch/{workflow_id}`.
  - `DockerRunner` (Hardened): Menggunakan Docker Engine API untuk meluncurkan container ephemeral (`alpine` atau `python:3.12-slim`) dengan alokasi memori maksimal (128MB) dan auto-remove (`--rm`).

### 3.4. Package `pkg/orchestrator` (Core Durable Engine)
- **State Machine Lifecycle:**  
  `PENDING` $\rightarrow$ `RUNNING` $\rightarrow$ `STEP_EXECUTING` $\rightarrow$ `TOOL_EXECUTING` $\rightarrow$ `COMPLETED` | `FAILED` | `PAUSED`.
- **Durable Replay Loop:**
  1. Ambil seluruh event log dari `Store.GetEvents(workflowID)`.
  2. Rekonstruksi state memory tanpa mengeksekusi ulang LLM atau Tool yang sudah tercatat sukses (`COMPLETED`).
  3. Lanjutkan eksekusi dari step terakhir yang belum selesai.
- **Safety Circuit Breakers:**
  - `MaxSteps`: Default 10 steps per workflow run.
  - `MaxTokenBudget`: Default 8,000 tokens per workflow run.
  - `WorkflowTimeout`: Default 60 detik per workflow run.

### 3.5. Package `pkg/api` (Ingress Gateway)
- **REST Endpoints:**
  - `POST /api/v1/agents/execute` $\rightarrow$ Menerima goal, tool definitions, dan initial context.
  - `GET  /api/v1/workflows/{id}` $\rightarrow$ Mengembalikan status workflow, step terakhir, dan hasil akhir.
  - `GET  /api/v1/workflows/{id}/history` $\rightarrow$ Mengembalikan daftar lengkap event history & DAG execution.
  - `POST /api/v1/workflows/{id}/resume` $\rightarrow$ Memaksa kelanjutan workflow yang sempat gagal atau di-pause.
  - `GET  /healthz` $\rightarrow$ Liveness check.
  - `GET  /metrics` $\rightarrow$ Prometheus metrics endpoint.
- **MCP Protocol:**
  - `POST /mcp` $\rightarrow$ JSON-RPC 2.0 endpoint untuk Model Context Protocol tools & resource enumeration.

---

## 4. Database Schema (DDL)

```sql
-- Workflows Table
CREATE TABLE IF NOT EXISTS workflows (
    id VARCHAR(64) PRIMARY KEY,
    goal TEXT NOT NULL,
    status VARCHAR(32) NOT NULL,
    current_step INT NOT NULL DEFAULT 0,
    total_tokens_used INT NOT NULL DEFAULT 0,
    result TEXT,
    error_message TEXT,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

-- Workflow Event Sourcing Log Table
CREATE TABLE IF NOT EXISTS workflow_events (
    id VARCHAR(64) PRIMARY KEY,
    workflow_id VARCHAR(64) NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    step_number INT NOT NULL,
    event_type VARCHAR(64) NOT NULL, -- STEP_STARTED, LLM_PROMPTED, TOOL_CALLED, TOOL_COMPLETED, STEP_COMPLETED, WORKFLOW_FAILED
    payload_json TEXT NOT NULL,
    tokens_used INT NOT NULL DEFAULT 0,
    duration_ms INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_workflow_events_wid ON workflow_events(workflow_id, step_number);
```

---

## 5. Verification & Quality Assurance Strategy

1. **Unit Test Coverage:** Minimum 85% code coverage pada semua packages (`storage`, `llm`, `sandbox`, `orchestrator`, `api`) berjalan 100% offline via `MockDriver`.
2. **Deterministic Crash-Recovery Chaos Test:**
   - Test harness mengeksekusi workflow 4-step.
   - Pada event `STEP_3_STARTED`, harness mematikan context / membatalkan proses.
   - Instance engine baru diinisialisasi terhadap database yang sama.
   - Memanggil `Resume()` dan memverifikasi bahwa Step 1 & 2 tidak diulang (*zero duplicate execution*), dan Step 3 & 4 sukses diselesaikan.
3. **Security Test (Env Leakage Protection):**
   - Tool script mencoba membaca `os.Environ()` untuk mencari string `SECRET_KEY` atau `API_KEY` host.
   - Assert bahwa output kosong / tersanitasi.

---

## 6. Directory Structure Blueprint

```text
aegis-runtime/
├── cmd/
│   └── server/
│       └── main.go
├── pkg/
│   ├── domain/                     # Entities & Domain Models
│   ├── storage/                    # SQLite & Postgres Stores + Migrations
│   │   └── migrations/
│   │       └── 001_init.sql
│   ├── llm/                        # MockDriver & OpenAICompatibleDriver
│   ├── sandbox/                    # ProcessRunner & DockerRunner
│   ├── orchestrator/               # Durable State Machine & Replay Engine
│   └── api/                        # REST & MCP HTTP Handlers
├── configs/
│   └── aegis.yaml
├── tests/
│   ├── unit/
│   └── chaos/
├── Dockerfile
└── go.mod
```
