//go:build !linux

package socket

func detectLeiKwan(root string) *LeiKwanReport { return nil }
