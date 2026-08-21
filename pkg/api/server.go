package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
	"github.com/aria-tec/aegis-runtime/pkg/orchestrator"
	"github.com/aria-tec/aegis-runtime/pkg/storage"
)

// Server represents the Aegis HTTP ingress gateway providing REST and Model Context Protocol (MCP) APIs.
type Server struct {
	engine *orchestrator.Engine
	store  storage.Store
	mux    *http.ServeMux
}

// NewServer constructs and initializes a new Server with registered routes.
func NewServer(engine *orchestrator.Engine, store storage.Store) *Server {
	s := &Server{
		engine: engine,
		store:  store,
		mux:    http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

// Handler returns the underlying http.Handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// ServeHTTP satisfies the http.Handler interface directly.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("POST /api/v1/agents/execute", s.handleExecuteAgent)
	s.mux.HandleFunc("GET /api/v1/workflows/{id}", s.handleGetWorkflow)
	s.mux.HandleFunc("GET /api/v1/workflows/{id}/history", s.handleGetWorkflowHistory)
	s.mux.HandleFunc("POST /api/v1/workflows/{id}/resume", s.handleResumeWorkflow)
	s.mux.HandleFunc("POST /mcp", s.handleMCP)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "healthy",
		"service": "aegis-runtime",
	})
}

type executeAgentRequest struct {
	ID          string `json:"id"`
	Goal        string `json:"goal"`
	MaxSteps    int    `json:"max_steps"`
	TokenBudget int    `json:"token_budget"`
}

func (s *Server) handleExecuteAgent(w http.ResponseWriter, r *http.Request) {
	var req executeAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if req.Goal == "" {
		http.Error(w, "goal is required", http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		req.ID = fmt.Sprintf("wf_%d", r.Context().Value("id"))
	}

	wf, err := s.engine.StartWorkflow(r.Context(), req.ID, req.Goal, req.MaxSteps, req.TokenBudget)
	if err != nil {
		http.Error(w, fmt.Sprintf("Workflow execution failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(wf)
}

func (s *Server) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	id := s.extractWorkflowID(r)
	if id == "" {
		http.Error(w, "Workflow ID required", http.StatusBadRequest)
		return
	}

	wf, err := s.store.GetWorkflow(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf("Workflow not found: %v", err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(wf)
}

func (s *Server) handleGetWorkflowHistory(w http.ResponseWriter, r *http.Request) {
	id := s.extractWorkflowID(r)
	if id == "" {
		http.Error(w, "Workflow ID required", http.StatusBadRequest)
		return
	}

	events, err := s.store.GetEvents(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load workflow history: %v", err), http.StatusInternalServerError)
		return
	}

	if events == nil {
		events = make([]domain.WorkflowEvent, 0)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"workflow_id": id,
		"events":      events,
	})
}

func (s *Server) handleResumeWorkflow(w http.ResponseWriter, r *http.Request) {
	id := s.extractWorkflowID(r)
	if id == "" {
		http.Error(w, "Workflow ID required", http.StatusBadRequest)
		return
	}

	wf, err := s.engine.ResumeWorkflow(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf("Workflow resumption failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(wf)
}

func (s *Server) extractWorkflowID(r *http.Request) string {
	if id := r.PathValue("id"); id != "" {
		return id
	}
	// Fallback extraction
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/workflows/")
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// Model Context Protocol (MCP) JSON-RPC 2.0 Structures
type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *mcpError   `json:"error,omitempty"`
}

type mcpError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	var req mcpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mcpResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error: &mcpError{
				Code:    -32700,
				Message: "Parse error: invalid JSON",
			},
		})
		return
	}

	resp := mcpResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{
					"listChanged": false,
				},
			},
			"serverInfo": map[string]interface{}{
				"name":    "aegis-runtime",
				"version": "1.0.0",
			},
		}

	case "ping":
		resp.Result = map[string]interface{}{}

	case "tools/list":
		resp.Result = map[string]interface{}{
			"tools": []map[string]interface{}{
				{
					"name":        "execute_agent",
					"description": "Execute a new agent workflow with goal and constraints",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"id":           map[string]interface{}{"type": "string", "description": "Unique workflow identifier"},
							"goal":         map[string]interface{}{"type": "string", "description": "Goal or instruction for the agent"},
							"max_steps":    map[string]interface{}{"type": "integer", "description": "Maximum steps limit"},
							"token_budget": map[string]interface{}{"type": "integer", "description": "Total token budget limit"},
						},
						"required": []string{"id", "goal"},
					},
				},
				{
					"name":        "get_workflow",
					"description": "Fetch status, current step, and results of an existing workflow",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"id": map[string]interface{}{"type": "string", "description": "Workflow identifier"},
						},
						"required": []string{"id"},
					},
				},
				{
					"name":        "resume_workflow",
					"description": "Resume execution of an interrupted or paused workflow",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"id": map[string]interface{}{"type": "string", "description": "Workflow identifier"},
						},
						"required": []string{"id"},
					},
				},
			},
		}

	case "tools/call":
		var callParams mcpToolCallParams
		if err := json.Unmarshal(req.Params, &callParams); err != nil {
			resp.Error = &mcpError{Code: -32602, Message: "Invalid params for tools/call"}
			break
		}

		switch callParams.Name {
		case "execute_agent":
			var args executeAgentRequest
			if err := json.Unmarshal(callParams.Arguments, &args); err != nil {
				resp.Error = &mcpError{Code: -32602, Message: "Invalid arguments for execute_agent"}
				break
			}
			wf, err := s.engine.StartWorkflow(r.Context(), args.ID, args.Goal, args.MaxSteps, args.TokenBudget)
			if err != nil {
				resp.Result = map[string]interface{}{
					"content": []map[string]interface{}{
						{"type": "text", "text": fmt.Sprintf("Error: %v", err)},
					},
					"isError": true,
				}
			} else {
				wfJSON, _ := json.Marshal(wf)
				resp.Result = map[string]interface{}{
					"content": []map[string]interface{}{
						{"type": "text", "text": string(wfJSON)},
					},
					"isError": false,
				}
			}

		case "get_workflow":
			var args struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(callParams.Arguments, &args); err != nil {
				resp.Error = &mcpError{Code: -32602, Message: "Invalid arguments for get_workflow"}
				break
			}
			wf, err := s.store.GetWorkflow(r.Context(), args.ID)
			if err != nil {
				resp.Result = map[string]interface{}{
					"content": []map[string]interface{}{
						{"type": "text", "text": fmt.Sprintf("Error: %v", err)},
					},
					"isError": true,
				}
			} else {
				wfJSON, _ := json.Marshal(wf)
				resp.Result = map[string]interface{}{
					"content": []map[string]interface{}{
						{"type": "text", "text": string(wfJSON)},
					},
					"isError": false,
				}
			}

		case "resume_workflow":
			var args struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(callParams.Arguments, &args); err != nil {
				resp.Error = &mcpError{Code: -32602, Message: "Invalid arguments for resume_workflow"}
				break
			}
			wf, err := s.engine.ResumeWorkflow(r.Context(), args.ID)
			if err != nil {
				resp.Result = map[string]interface{}{
					"content": []map[string]interface{}{
						{"type": "text", "text": fmt.Sprintf("Error: %v", err)},
					},
					"isError": true,
				}
			} else {
				wfJSON, _ := json.Marshal(wf)
				resp.Result = map[string]interface{}{
					"content": []map[string]interface{}{
						{"type": "text", "text": string(wfJSON)},
					},
					"isError": false,
				}
			}

		default:
			resp.Result = map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": fmt.Sprintf("Unknown tool: %s", callParams.Name)},
				},
				"isError": true,
			}
		}

	default:
		resp.Error = &mcpError{
			Code:    -32601,
			Message: fmt.Sprintf("Method not found: %s", req.Method),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
