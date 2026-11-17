//go:build !linux

package socket

import (
	"context"
	"errors"
)

// The 利群主机 module only exists on linux (the LeiKwan client is linux only).
type lkHostManager struct{}

func newLKHostManager(string) *lkHostManager            { return nil }
func (m *lkHostManager) runLoop(context.Context)        {}
func (m *lkHostManager) takeReport() *LeiKwanHostReport { return nil }
func (m *lkHostManager) handleSync(context.Context, interface{}) (*LeiKwanHostReport, error) {
	return nil, errors.New("利群主机模块只在 linux 上运行")
}
func (m *lkHostManager) handleDiag(context.Context, interface{}) (*LeiKwanDiagResponse, error) {
	return nil, errors.New("利群主机模块只在 linux 上运行")
}
