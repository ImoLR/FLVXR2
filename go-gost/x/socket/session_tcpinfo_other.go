//go:build !linux

package socket

import "github.com/gorilla/websocket"

func tcpInfoSummary(conn *websocket.Conn) string { return "" }
