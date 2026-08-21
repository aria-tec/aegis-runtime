CREATE TABLE IF NOT EXISTS workflows (
    id VARCHAR(64) PRIMARY KEY,
    goal TEXT NOT NULL,
    status VARCHAR(32) NOT NULL,
    current_step INT NOT NULL DEFAULT 0,
    total_tokens_used INT NOT NULL DEFAULT 0,
    max_steps INT NOT NULL DEFAULT 10,
    token_budget INT NOT NULL DEFAULT 8000,
    result TEXT,
    error_message TEXT,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS workflow_events (
    id VARCHAR(64) PRIMARY KEY,
    workflow_id VARCHAR(64) NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    step_number INT NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    payload_json TEXT NOT NULL,
    tokens_used INT NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_workflow_events_wid ON workflow_events(workflow_id, step_number);
