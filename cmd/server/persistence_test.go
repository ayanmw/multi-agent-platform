package main

import (
	"sync"
	"testing"
)

// TestNewTaskIDUnique verifies that rapid concurrent calls to newTaskID never
// produce duplicate IDs.
func TestNewTaskIDUnique(t *testing.T) {
	const n = 1000
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ids[idx] = newTaskID()
		}(i)
	}
	wg.Wait()

	seen := make(map[string]struct{}, n)
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate task_id generated: %s", id)
		}
		seen[id] = struct{}{}
	}
}
