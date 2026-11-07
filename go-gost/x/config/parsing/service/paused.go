package service

import (
	"net"

	"github.com/go-gost/core/service"
	"github.com/go-gost/x/config"
)

// Plan 236: a service whose config carries metadata `paused: true` (written by
// PauseService, or sent by the panel for a paused rule) must not listen after an
// agent restart, a config reload or an UpdateService/AddService. It is
// registered as a PausedService instead: pause/resume/delete find it by name,
// but it binds nothing. ResumeService replaces it with the parsed service.

// IsPaused reports whether the service config is marked paused.
func IsPaused(cfg *config.ServiceConfig) bool {
	if cfg == nil || cfg.Metadata == nil {
		return false
	}
	// Same test as PauseService/ResumeService (`paused == true`).
	v, ok := cfg.Metadata["paused"].(bool)
	return ok && v
}

// ParseServiceOrPaused parses cfg, or returns the closed placeholder when cfg is
// marked paused (create/update paths).
func ParseServiceOrPaused(cfg *config.ServiceConfig) (service.Service, error) {
	if IsPaused(cfg) {
		return NewPausedService(cfg), nil
	}
	return ParseService(cfg)
}

// PausedService is the registry placeholder of a paused service.
type PausedService struct {
	addr pausedAddr
}

// NewPausedService returns the placeholder for a paused service config.
func NewPausedService(cfg *config.ServiceConfig) service.Service {
	addr := ""
	if cfg != nil {
		addr = cfg.Addr
	}
	return &PausedService{addr: pausedAddr(addr)}
}

// Serve does nothing: a paused service accepts no connections.
func (s *PausedService) Serve() error { return nil }

// Addr is the configured address (nothing is bound to it).
func (s *PausedService) Addr() net.Addr { return s.addr }

// Close does nothing: nothing was opened.
func (s *PausedService) Close() error { return nil }

// Paused marks the placeholder (inventory, tests).
func (s *PausedService) Paused() bool { return true }

// IsPausedService reports whether svc is a paused placeholder.
func IsPausedService(svc service.Service) bool {
	p, ok := svc.(interface{ Paused() bool })
	return ok && p.Paused()
}

type pausedAddr string

func (a pausedAddr) Network() string { return "tcp" }
func (a pausedAddr) String() string  { return string(a) }
