package policy

import (
	"testing"
	"time"
)

func FuzzEngineNeverSelectsUnknownOrDuplicateTasks(f *testing.F) {
	f.Add("a", "b", 50, 60)
	f.Add("same", "same", 0, 100)
	f.Fuzz(func(t *testing.T, firstID, secondID string, firstPriority, secondPriority int) {
		if firstPriority < 0 || firstPriority > 100 || secondPriority < 0 || secondPriority > 100 {
			t.Skip()
		}
		config := DefaultConfig()
		config.MaxConcurrent = 2
		engine, err := NewEngine(config)
		if err != nil {
			t.Fatal(err)
		}
		snapshot := Snapshot{
			At: fixedNow,
			Ready: []Task{
				testTask(firstID, fixedNow.Add(-time.Minute), firstPriority),
				testTask(secondID, fixedNow.Add(-time.Minute), secondPriority),
			},
		}
		decision, err := engine.Decide(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		known := map[string]bool{firstID: true, secondID: true}
		selected := map[string]bool{}
		for _, task := range decision.Selected {
			if !known[task.ID] {
				t.Fatalf("selected unknown task %q", task.ID)
			}
			if selected[task.ID] {
				t.Fatalf("selected duplicate task %q", task.ID)
			}
			selected[task.ID] = true
		}
		if len(decision.Selected) > config.MaxConcurrent {
			t.Fatalf("selected %d tasks, max is %d", len(decision.Selected), config.MaxConcurrent)
		}
	})
}
