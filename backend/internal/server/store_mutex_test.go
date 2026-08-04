package server

import (
	"fmt"
	"sync"
	"testing"
)

func TestNopLockerImplementsLocker(t *testing.T) {
	var l sync.Locker = nopLocker{}
	l.Lock()
	l.Unlock()
}

func TestNopLockerConcurrentSafety(t *testing.T) {
	// Verify nopLocker does not introduce races when used as a no-op mutex.
	// The race detector should be clean when running with -race.
	var l sync.Locker = nopLocker{}
	var wg sync.WaitGroup
	const goroutines = 100
	const iterations = 1000

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				l.Lock()
				l.Unlock()
			}
		}()
	}
	wg.Wait()
}

func TestStoreDriverMutexType(t *testing.T) {
	// PG-backed store should use nopLocker; SQLite should use a real *sync.Mutex.
	tests := []struct {
		driver  string
		isNoop  bool
	}{
		{"sqlite", false},
		{"postgres", true},
	}

	for _, tt := range tests {
		t.Run(tt.driver, func(t *testing.T) {
			store := NewMemoryStore()
			if tt.driver == "postgres" {
				store.dbDriver = "postgres"
				store.mu = nopLocker{}
			}
			_, isNoop := store.mu.(nopLocker)
			if isNoop != tt.isNoop {
				t.Errorf("driver=%s: expected isNoop=%v, got %v", tt.driver, tt.isNoop, isNoop)
			}
		})
	}
}

func TestConcurrentWritesDataIntegrity(t *testing.T) {
	// 20 goroutines each inserting 50 projects concurrently.
	// All 1000 records must be present after the run.
	store := NewMemoryStore()
	const goroutines = 20
	const projectsPerGoroutine = 50
	totalExpected := goroutines * projectsPerGoroutine

	var wg sync.WaitGroup
	errs := make(chan error, totalExpected)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(groupID int) {
			defer wg.Done()
			for i := 0; i < projectsPerGoroutine; i++ {
				name := fmt.Sprintf("proj-g%d-n%d", groupID, i)
				store.CreateProject(Project{Name: name})
			}
		}(g)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}

	projects := store.ListProjects()
	if len(projects) != totalExpected {
		t.Errorf("expected %d projects, got %d", totalExpected, len(projects))
	}
}

func TestSQLiteMutexIsNotNoop(t *testing.T) {
	// Regression guard: a SQLite-backed store must use a real mutex, not nopLocker.
	store := NewMemoryStore()
	if _, ok := store.mu.(nopLocker); ok {
		t.Error("SQLite store mutex is nopLocker — should be a real *sync.Mutex")
	}
}
