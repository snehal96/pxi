//go:build windows

package main

// The pxi terminal needs Unix pseudo-terminals; on Windows it is off.
func (s *Server) registerPxiTermRoutes() {}

// The Claude panel uses Unix process groups; on Windows it is off.
func (s *Server) registerPxiClaudeRoutes() {}
