//go:build !linux

package socket

import "errors"

func landingPeerSamples(port int) ([]landingPeerSample, error) {
	return nil, errors.New("只支持 Linux")
}
