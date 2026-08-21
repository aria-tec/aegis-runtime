package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aria-tec/aegis-runtime/pkg/domain"
	"github.com/aria-tec/aegis-runtime/pkg/storage"
)

func TestSQLiteStore_WorkflowLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_aegis.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	wf := domain.NewWorkflow("wf-test-1", "Automate Inventory", 10, 8000)

	// Test CreateWorkflow
	if err := store.CreateWorkflow(ctx, wf); err != nil {
		t.Fatalf("failed to create workflow: %v", err)
	}

	// Test GetWorkflow
	fetched, err := store.GetWorkflow(ctx, "wf-test-1")
	if err != nil {
		t.Fatalf("failed to get workflow: %v", err)
	}
	if fetched.ID != "wf-test-1" {
		t.Fatalf("expected id 'wf-test-1', got '%s'", fetched.ID)
	}
	if fetched.Goal != "Automate Inventory" {
		t.Fatalf("expected goal 'Automate Inventory', got '%s'", fetched.Goal)
	}
	if fetched.Status != domain.StatusPending {
		t.Fatalf("expected status 'PENDING', got '%s'", fetched.Status)
	}
	if fetched.MaxSteps != 10 || fetched.TokenBudget != 8000 {
		t.Fatalf("unexpected limits: max_steps=%d, token_budget=%d", fetched.MaxSteps, fetched.TokenBudget)
	}

	// Test UpdateWorkflow
	fetched.Status = domain.StatusRunning
	fetched.CurrentStep = 1
	fetched.TotalTokens = 150
	fetched.Result = "partial result"
	fetched.ErrorMessage = ""
	if err := store.UpdateWorkflow(ctx, fetched); err != nil {
		t.Fatalf("failed to update workflow: %v", err)
	}

	updated, err := store.GetWorkflow(ctx, "wf-test-1")
	if err != nil {
		t.Fatalf("failed to get updated workflow: %v", err)
	}
	if updated.Status != domain.StatusRunning {
		t.Fatalf("expected status RUNNING, got %s", updated.Status)
	}
	if updated.CurrentStep != 1 {
		t.Fatalf("expected step 1, got %d", updated.CurrentStep)
	}
	if updated.TotalTokens != 150 {
		t.Fatalf("expected tokens 150, got %d", updated.TotalTokens)
	}
	if updated.Result != "partial result" {
		t.Fatalf("expected result 'partial result', got '%s'", updated.Result)
	}

	// Test Update non-existent workflow
	nonExistentWf := domain.NewWorkflow("wf-non-existent", "Ghost", 5, 1000)
	if err := store.UpdateWorkflow(ctx, nonExistentWf); err == nil {
		t.Fatalf("expected error updating non-existent workflow, got nil")
	}

	// Test AppendEvent and GetEvents
	evt1 := domain.WorkflowEvent{
		ID:          "evt-1",
		WorkflowID:  "wf-test-1",
		StepNumber:  1,
		EventType:   domain.EventStepStarted,
		PayloadJSON: `{"step": 1}`,
		TokensUsed:  50,
		DurationMs:  10,
		CreatedAt:   time.Now().UTC(),
	}
	evt2 := domain.WorkflowEvent{
		ID:          "evt-2",
		WorkflowID:  "wf-test-1",
		StepNumber:  1,
		EventType:   domain.EventStepCompleted,
		PayloadJSON: `{"status": "ok"}`,
		TokensUsed:  100,
		DurationMs:  25,
		CreatedAt:   time.Now().UTC().Add(time.Millisecond * 10),
	}

	if err := store.AppendEvent(ctx, &evt1); err != nil {
		t.Fatalf("failed to append event 1: %v", err)
	}
	if err := store.AppendEvent(ctx, &evt2); err != nil {
		t.Fatalf("failed to append event 2: %v", err)
	}

	events, err := store.GetEvents(ctx, "wf-test-1")
	if err != nil {
		t.Fatalf("failed to get events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].ID != "evt-1" || events[0].EventType != domain.EventStepStarted {
		t.Fatalf("unexpected event 0: %+v", events[0])
	}
	if events[1].ID != "evt-2" || events[1].EventType != domain.EventStepCompleted {
		t.Fatalf("unexpected event 1: %+v", events[1])
	}
	if events[0].TokensUsed != 50 || events[1].TokensUsed != 100 {
		t.Fatalf("tokens mismatch in events: %+v", events)
	}

	// Test GetWorkflow Not Found
	_, err = store.GetWorkflow(ctx, "non-existent-wf")
	if err == nil {
		t.Fatalf("expected error for non-existent workflow, got nil")
	}

	// Test GetEvents for non-existent workflow returns empty slice
	emptyEvents, err := store.GetEvents(ctx, "non-existent-wf")
	if err != nil {
		t.Fatalf("unexpected error getting events for empty workflow: %v", err)
	}
	if len(emptyEvents) != 0 {
		t.Fatalf("expected 0 events, got %d", len(emptyEvents))
	}
}

func TestSQLiteStore_EventOrderingAndIsolation(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_order.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	wf1 := domain.NewWorkflow("wf-1", "Goal 1", 10, 5000)
	wf2 := domain.NewWorkflow("wf-2", "Goal 2", 10, 5000)

	if err := store.CreateWorkflow(ctx, wf1); err != nil {
		t.Fatalf("failed to create wf1: %v", err)
	}
	if err := store.CreateWorkflow(ctx, wf2); err != nil {
		t.Fatalf("failed to create wf2: %v", err)
	}

	now := time.Now().UTC()
	// Insert events out of order for wf1
	eventsWf1 := []domain.WorkflowEvent{
		{ID: "e3", WorkflowID: "wf-1", StepNumber: 2, EventType: domain.EventStepCompleted, PayloadJSON: "{}", CreatedAt: now.Add(time.Second * 3)},
		{ID: "e1", WorkflowID: "wf-1", StepNumber: 1, EventType: domain.EventStepStarted, PayloadJSON: "{}", CreatedAt: now.Add(time.Second * 1)},
		{ID: "e2", WorkflowID: "wf-1", StepNumber: 1, EventType: domain.EventToolCalled, PayloadJSON: "{}", CreatedAt: now.Add(time.Second * 2)},
	}
	for _, e := range eventsWf1 {
		if err := store.AppendEvent(ctx, &e); err != nil {
			t.Fatalf("failed to append event to wf1: %v", err)
		}
	}

	// Insert event for wf2
	eventWf2 := domain.WorkflowEvent{
		ID:          "e-wf2",
		WorkflowID:  "wf-2",
		StepNumber:  1,
		EventType:   domain.EventStepStarted,
		PayloadJSON: "{}",
		CreatedAt:   now,
	}
	if err := store.AppendEvent(ctx, &eventWf2); err != nil {
		t.Fatalf("failed to append event to wf2: %v", err)
	}

	// Verify wf1 events are sorted: e1 (step 1, t1), e2 (step 1, t2), e3 (step 2, t3)
	gotWf1Events, err := store.GetEvents(ctx, "wf-1")
	if err != nil {
		t.Fatalf("failed to get wf1 events: %v", err)
	}
	if len(gotWf1Events) != 3 {
		t.Fatalf("expected 3 events for wf1, got %d", len(gotWf1Events))
	}
	if gotWf1Events[0].ID != "e1" || gotWf1Events[1].ID != "e2" || gotWf1Events[2].ID != "e3" {
		t.Fatalf("events not ordered correctly: got [%s, %s, %s]", gotWf1Events[0].ID, gotWf1Events[1].ID, gotWf1Events[2].ID)
	}

	// Verify wf2 events are isolated
	gotWf2Events, err := store.GetEvents(ctx, "wf-2")
	if err != nil {
		t.Fatalf("failed to get wf2 events: %v", err)
	}
	if len(gotWf2Events) != 1 || gotWf2Events[0].ID != "e-wf2" {
		t.Fatalf("expected only e-wf2 for wf2, got: %+v", gotWf2Events)
	}
}

func TestSQLiteStore_ForeignKeyEnforcement(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_fk.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	// Appending an event for a non-existent workflow should fail because of foreign keys
	evt := domain.WorkflowEvent{
		ID:          "orphan-evt",
		WorkflowID:  "ghost-wf",
		StepNumber:  1,
		EventType:   domain.EventStepStarted,
		PayloadJSON: "{}",
		CreatedAt:   time.Now().UTC(),
	}
	err = store.AppendEvent(ctx, &evt)
	if err == nil {
		t.Fatalf("expected foreign key violation error, got nil")
	}
}
