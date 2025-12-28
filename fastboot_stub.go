//go:build nofastboot

package tensorutils

import (
	"fmt"
	"time"
)

type Fastboot struct{}

func GetFastboot(targetSerial string) (*Fastboot, error) {
	return nil, fmt.Errorf("fastboot support disabled (built with -tags nofastboot)")
}

func (fb *Fastboot) GetVar(name string) (string, error) {
	return "", fmt.Errorf("fastboot not available")
}

func (fb *Fastboot) GetVarWithTimeout(name string, timeout time.Duration) (string, error) {
	return "", fmt.Errorf("fastboot not available")
}

func (fb *Fastboot) IsFlashing() bool {
	return false
}

func (fb *Fastboot) GetSerial() string {
	return ""
}

func (fb *Fastboot) Close() error {
	return nil
}
