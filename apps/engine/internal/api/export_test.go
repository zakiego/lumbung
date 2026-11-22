package api

// MaxWaitsPerToken is maxWaitsPerToken, for the external tests.
const MaxWaitsPerToken = maxWaitsPerToken

// WaitersHeld is how many waits the server counts as open, across tokens.
func (s *Server) WaitersHeld() int {
	s.waiters.mu.Lock()
	defer s.waiters.mu.Unlock()
	n := 0
	for _, v := range s.waiters.n {
		n += v
	}
	return n
}
