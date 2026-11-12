package server

import "sync"

// Waiting is optional and bounded independently of model invocation capacity.
func (s *Server) acquireAllowanceWaiter(key string) (func(), bool) {
	s.allowanceWaitMu.Lock()
	defer s.allowanceWaitMu.Unlock()
	if s.allowanceWaitCount >= 128 || s.allowanceWaiters[key] >= 4 {
		return nil, false
	}
	if s.allowanceWaiters == nil {
		s.allowanceWaiters = map[string]int{}
	}
	s.allowanceWaitCount++
	s.allowanceWaiters[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.allowanceWaitMu.Lock()
			defer s.allowanceWaitMu.Unlock()
			s.allowanceWaitCount--
			s.allowanceWaiters[key]--
			if s.allowanceWaiters[key] == 0 {
				delete(s.allowanceWaiters, key)
			}
		})
	}, true
}
