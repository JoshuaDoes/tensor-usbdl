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

func (fb *Fastboot) OemCommand(cmd string) ([]string, error) {
	return nil, fmt.Errorf("fastboot not available")
}

func (fb *Fastboot) OemCommandWithTimeout(cmd string, timeout time.Duration) ([]string, error) {
	return nil, fmt.Errorf("fastboot not available")
}

func (fb *Fastboot) IsFlashing() bool {
	return false
}

func (fb *Fastboot) GetSerial() string {
	return ""
}

func (fb *Fastboot) GetSpeed() string {
	return ""
}

func (fb *Fastboot) Reboot() error {
	return fmt.Errorf("fastboot not available")
}

func (fb *Fastboot) PowerOff() error {
	return fmt.Errorf("fastboot not available")
}

func (fb *Fastboot) FlashingUnlock() error {
	return fmt.Errorf("fastboot not available")
}

func (fb *Fastboot) Reset() error {
	return fmt.Errorf("fastboot not available")
}

func (fb *Fastboot) Close() error {
	return nil
}
