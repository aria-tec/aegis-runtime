# 🛡️ Aegis-Runtime

[![Go Version](https://img.shields.io/badge/Go-1.27+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Build Status](https://img.shields.io/badge/Tests-100%25%20Passing-success?style=flat)](tests/)
[![Zero CGO](https://img.shields.io/badge/CGO-Disabled%20(Zero%20CGO)-blue?style=flat)](pkg/storage/)
[![Release](https://img.shields.io/badge/Release-v1.0.0--frozen-cyan?style=flat)](https://github.com/aria-tec/aegis-runtime)
[![Security Invariants](https://img.shields.io/badge/Security-Hardened%20(Govulncheck%20%2B%20Fuzzing)-green?style=flat)](.github/workflows/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

**Aegis-Runtime** is an enterprise-grade, standalone, fault-tolerant **AI Agent Execution Engine & Sandboxed Tool Gateway** written in pure Go. It bridges the gap between non-deterministic Large Language Models and mission-critical backend systems by providing **Durable Event-Sourced State Machine Replay**, **Zero-Leak Process/Docker Sandboxing**, and **Universal REST & Model Context Protocol (MCP) Ingress**.

---

## 🌟 Why Aegis-Runtime?

In modern software systems (2026), deploying autonomous multi-step AI Agents in production faces three critical infrastructure bottlenecks:
1. **Unreliable Multi-Step Execution:** If a server crashes at Step 4 of a complex reasoning workflow, traditional libraries lose state and waste thousands of dollars re-prompting from Step 1. **Aegis-Runtime provides deterministic event-sourced replay**, resuming execution from the exact point of failure with **zero duplicate tool executions**.
2. **Security & Credential Leaks:** AI agents generating and executing arbitrary Python/Shell scripts can compromise host credentials. **Aegis-Runtime enforces strict host environment scrubbing** and sandboxed micro-containers.
3. **Framework Lock-in:** Most AI agent frameworks are Python/JS-only libraries that bleed into application logic. **Aegis-Runtime runs as a standalone OCI container / daemon**, callable from **.NET, Go, Node.js, Rust, or Python** via standard REST and MCP.

---

## 🏛️ System Architecture

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

## ✨ Key Features

* **⚡ Ultra-Lightweight & Zero-CGO:** Built with pure Go (`modernc.org/sqlite`). Cross-compiles to Linux, macOS, and Windows with `CGO_ENABLED=0`.
* **🔄 Durable Event Sourcing:** Immutable step history (`WORKFLOW_STARTED`, `STEP_STARTED`, `LLM_PROMPTED`, `TOOL_CALLED`, `TOOL_COMPLETED`, `WORKFLOW_COMPLETED`).
* **🛡️ Sandboxed Tool Execution:** 
  * `ProcessRunner`: 1–5ms execution with strict host environment scrubbing (wipes host `AWS_KEY` / `API_KEY` before execution).
  * `Output Bomb Protection`: Bounded 1MB buffer (`io.LimitReader`) preventing OOM denial-of-service from infinite stdout/stderr loops.
  * `DockerRunner`: Ephemeral container isolation (`alpine`, `python:3.12-slim`) with memory quotas (128MB) and CPU limits.
* **🧠 Hybrid Dual-Driver LLM Support:**
  * `MockDriver`: Deterministic scriptable reasoning for offline tests and instant CI/CD.
  * `OpenAICompatibleDriver`: Native support for **Google Gemini**, **OpenAI**, **DeepSeek**, **Groq**, and **Local Ollama/vLLM**.
* **🔌 Polyglot Ingress Gateway:**
  * Clean HTTP REST API.
  * **Model Context Protocol (MCP)** JSON-RPC 2.0 endpoint (`POST /mcp`) for standardized AI tool discovery.
* **🚦 Safety Circuit Breakers:** Configurable maximum step thresholds and token budget limits to prevent runaway reasoning loops.

---

## 🚀 Quickstart

### 1. Run Standalone Daemon (Zero Dependencies)

No Docker or external database required! SQLite database is automatically created and migrated on startup:

```bash
# Clone and run
git clone https://github.com/aria-tec/aegis-runtime.git
cd aegis-runtime
go run ./cmd/server
```
Server boots on `http://localhost:8085` using local database `data/aegis.db`.

### 2. Connect to Google Gemini / OpenAI

```bash
# Example with Google Gemini 2.5 Flash
GEMINI_API_KEY="your-gemini-api-key" \
LLM_PROVIDER="openai" \
LLM_BASE_URL="https://generativelanguage.googleapis.com/v1beta/openai/" \
LLM_MODEL="gemini-2.5-flash" \
go run ./cmd/server
```

```bash
# Example with OpenAI GPT-4o-mini
OPENAI_API_KEY="sk-..." \
LLM_PROVIDER="openai" \
LLM_BASE_URL="https://api.openai.com/v1" \
LLM_MODEL="gpt-4o-mini" \
go run ./cmd/server
```

---

## 📡 REST API Reference

### Execute an Agent Workflow
```bash
curl -X POST http://localhost:8085/api/v1/agents/execute \
  -H "Content-Type: application/json" \
  -d '{
    "id": "wf-inventory-101",
    "goal": "Analyze why SKU-999 had a 40% inventory drop and suggest remediation",
    "max_steps": 5,
    "token_budget": 4000
  }'
```

**Response (200 OK):**
```json
{
  "id": "wf-inventory-101",
  "goal": "Analyze why SKU-999 had a 40% inventory drop and suggest remediation",
  "status": "COMPLETED",
  "current_step": 1,
  "total_tokens": 656,
  "max_steps": 5,
  "token_budget": 4000,
  "result": "Identified seasonal demand spike. Recommended safety stock increase by 25%.",
  "created_at": "2026-08-21T02:13:08Z",
  "updated_at": "2026-08-21T02:13:12Z"
}
```

### Inspect Event Sourcing History
```bash
curl http://localhost:8085/api/v1/workflows/wf-inventory-101/history
```

**Response (200 OK):**
```json
{
  "workflow_id": "wf-inventory-101",
  "events": [
    {
      "id": "evt_wf-inventory-101_0_...",
      "step_number": 0,
      "event_type": "WORKFLOW_STARTED",
      "payload_json": "{\"goal\":\"Analyze why SKU-999...\"}",
      "tokens_used": 0,
      "duration_ms": 0,
      "created_at": "2026-08-21T02:13:08Z"
    },
    {
      "id": "evt_wf-inventory-101_1_...",
      "step_number": 1,
      "event_type": "LLM_PROMPTED",
      "payload_json": "{\"thought\":\"Analyzing warehouse logs...\",\"is_complete\":true}",
      "tokens_used": 656,
      "duration_ms": 4573,
      "created_at": "2026-08-21T02:13:12Z"
    },
    {
      "id": "evt_wf-inventory-101_1_...",
      "step_number": 1,
      "event_type": "WORKFLOW_COMPLETED",
      "payload_json": "{\"result\":\"Identified seasonal demand spike...\"}",
      "tokens_used": 0,
      "duration_ms": 0,
      "created_at": "2026-08-21T02:13:12Z"
    }
  ]
}
```

### Resume a Paused / Interrupted Workflow
```bash
curl -X POST http://localhost:8085/api/v1/workflows/wf-inventory-101/resume
```

---

## 🛡️ Active Vulnerability Hunter & Maintenance Framework ("The SQLite Standard")

Aegis-Runtime adheres to **The SQLite Standard** for long-term reliability and zero maintenance burden:

| Pillar | Mechanism | Security & Stability Guarantee |
|---|---|---|
| **Pillar 1: Bounded Memory & Subprocess Cleanup** | `io.LimitReader` (1MB limit) in `pkg/sandbox` | Defends against Tool Output Bomb OOM attacks; guarantees zero zombie child processes. |
| **Pillar 2: Continuous Fuzz Testing** | Native Go Fuzzing in `tests/fuzz_test.go` | Fuzzes Model Context Protocol (MCP) JSON-RPC parser and domain serialization against 100k+ mutated payloads. |
| **Pillar 3: Goroutine Leak Invariant Gate** | `go.uber.org/goleak` in `tests/leak_test.go` | Enforces zero dangling goroutines across completed workflows, cancelled contexts, and HTTP requests. |
| **Pillar 4: Automated Maintenance CI** | `.github/workflows/scheduled-maintenance.yml` | Weekly automated CVE scans via official `govulncheck`, static security analysis (`gosec`), and multi-version Go matrix testing. |

---

## 📜 Public Stability Charter

* **Frozen Core Invariants:** Aegis-Runtime v1.0.0 is architecturally complete and frozen. Future minor releases will never introduce breaking REST/MCP API changes.
* **Deterministic Bug Reproducibility:** Bug reports are submitted via [GitHub Issue Template](.github/ISSUE_TEMPLATE/bug_report.yml) requiring a minimal deterministic Go test case or cURL reproducer.

---

## 🧪 Testing & Verification

```bash
# Run all unit, chaos, and leak invariant tests with race detector
go test -race -cover ./...

# Run continuous Go fuzzers (30s per target)
go test -v -fuzz=FuzzDomainEventJSON -fuzztime=30s ./tests
go test -v -fuzz=FuzzMCPHandler -fuzztime=30s ./tests
```

---

## 📁 Repository Structure

```text
aegis-runtime/
├── .github/
│   ├── workflows/
│   │   └── scheduled-maintenance.yml # Weekly govulncheck, gosec & fuzzing CI
│   └── ISSUE_TEMPLATE/
│       ├── bug_report.yml            # Deterministic bug reproducer template
│       └── config.yml
├── cmd/
│   └── server/
│       └── main.go                   # Standalone daemon entrypoint (:8085)
├── pkg/
│   ├── domain/                       # Core domain entities & event types
│   ├── storage/                      # SQLite & Postgres adapters + embedded migrations
│   ├── llm/                          # MockDriver & OpenAICompatibleDriver
│   ├── sandbox/                      # ProcessRunner (env scrubbing & output bomb guard) & DockerRunner
│   ├── orchestrator/                 # Durable State Machine & Replay Engine
│   └── api/                          # REST & Model Context Protocol (MCP) handlers
├── tests/
│   ├── chaos/                        # Crash-recovery & replay validation tests
│   ├── fuzz_test.go                  # Go native fuzz testing suite
│   └── leak_test.go                  # Goleak goroutine & resource invariant tests
├── configs/
│   └── aegis.yaml                    # Configuration template
├── Dockerfile                        # Standalone OCI multi-stage container
├── README.md                         # Comprehensive documentation & Stability Charter
└── go.mod
```

---

## 📄 License

Distributed under the MIT License. See `LICENSE` for more information.
