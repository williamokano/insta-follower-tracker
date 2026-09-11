package httpapi

// uiRoutes registers the web interface. It is defined separately so the JSON
// API can be served without it.
func (s *Server) uiRoutes() error { return nil }
