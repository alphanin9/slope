package store

import (
	"context"
	"testing"
	"time"

	"slope/internal/config"
	"slope/internal/model"
)

func TestClaimNextTaskLocksMachine(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.UpsertMachines(ctx, []config.MachineConfig{{Name: "win1", Platform: "windows", Snapshot: "clean", GuestEndpoint: "http://127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	task := model.Task{ID: "t1", SubmittedAt: time.Now().UTC(), Status: model.StatusPending, SampleRef: "abc", VMName: "win1", TimeoutSec: 30}
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	claimed, machine, err := s.ClaimNextTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || machine == nil {
		t.Fatal("expected claimed task and machine")
	}
	if claimed.Status != model.StatusRunning || !machine.Locked {
		t.Fatalf("unexpected claim state: task=%s locked=%v", claimed.Status, machine.Locked)
	}
	again, _, err := s.ClaimNextTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again != nil {
		t.Fatal("expected no second claim")
	}
	if err := s.ReleaseMachine(ctx, "win1"); err != nil {
		t.Fatal(err)
	}
	m, err := s.Machine(ctx, "win1")
	if err != nil {
		t.Fatal(err)
	}
	if m.Locked {
		t.Fatal("machine should be released")
	}
}
