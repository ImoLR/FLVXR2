//go:build !linux

package nftables

func IPv6RAStatus() (string, string) { return "", "" }
