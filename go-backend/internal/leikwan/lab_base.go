//go:build leikwanlab

package leikwan

import "os"

// Plan 266 lab only (never in release builds): point the shared client at a
// local fake API, e.g. FLVX_LEIKWAN_LAB_URL=http://127.0.0.1:18466.
func init() { labBaseURL = os.Getenv("FLVX_LEIKWAN_LAB_URL") }
