//go:build !nofastboot

package tensorutils

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/gousb"
)

const (
	GoogleVID    = 0x18d1
	FastbootPID  = 0x4ee0
	FastbootPID2 = 0xd00d
)

type Fastboot struct {
	ctx    *gousb.Context
	dev    *gousb.Device
	intf   *gousb.Interface
	done   func()
	in     *gousb.InEndpoint
	out    *gousb.OutEndpoint
	serial string
	speed  string
}

// GetFastboot finds and opens a fastboot device by serial number
func GetFastboot(targetSerial string) (*Fastboot, error) {
	ctx := gousb.NewContext()

	// Find device with matching VID/PID
	var dev *gousb.Device
	var err error

	devs, err := ctx.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		if desc.Vendor == GoogleVID && (desc.Product == FastbootPID || desc.Product == FastbootPID2) {
			return true
		}
		return false
	})
	if err != nil {
		ctx.Close()
		return nil, fmt.Errorf("fastboot: failed to enumerate devices: %v", err)
	}

	if len(devs) == 0 {
		ctx.Close()
		return nil, fmt.Errorf("fastboot: no device found")
	}

	// Find device with matching serial or use first if no serial specified
	for _, d := range devs {
		serial, _ := d.SerialNumber()
		if targetSerial == "" || serial == targetSerial {
			dev = d
			break
		}
		d.Close()
	}

	// Close remaining devices
	for _, d := range devs {
		if d != dev {
			d.Close()
		}
	}

	if dev == nil {
		ctx.Close()
		return nil, fmt.Errorf("fastboot: device with serial %s not found", targetSerial)
	}

	serial, _ := dev.SerialNumber()

	// Set auto-detach kernel driver
	dev.SetAutoDetach(true)

	// Claim interface 0 (fastboot)
	intf, done, err := dev.DefaultInterface()
	if err != nil {
		dev.Close()
		ctx.Close()
		return nil, fmt.Errorf("fastboot: failed to claim interface: %v", err)
	}

	// Find bulk endpoints
	var in *gousb.InEndpoint
	var out *gousb.OutEndpoint
	for _, ep := range intf.Setting.Endpoints {
		if ep.Direction == gousb.EndpointDirectionIn {
			in, err = intf.InEndpoint(ep.Number)
			if err != nil {
				done()
				dev.Close()
				ctx.Close()
				return nil, fmt.Errorf("fastboot: failed to get IN endpoint: %v", err)
			}
		} else {
			out, err = intf.OutEndpoint(ep.Number)
			if err != nil {
				done()
				dev.Close()
				ctx.Close()
				return nil, fmt.Errorf("fastboot: failed to get OUT endpoint: %v", err)
			}
		}
	}

	if in == nil || out == nil {
		done()
		dev.Close()
		ctx.Close()
		return nil, fmt.Errorf("fastboot: endpoints not found")
	}

	return &Fastboot{
		ctx:    ctx,
		dev:    dev,
		intf:   intf,
		done:   done,
		in:     in,
		out:    out,
		serial: serial,
		speed:  dev.Desc.Speed.String(),
	}, nil
}

// GetVar sends "getvar:name" and returns the value
func (fb *Fastboot) GetVar(name string) (string, error) {
	return fb.GetVarWithTimeout(name, 5*time.Second)
}

// GetVarWithTimeout sends "getvar:name" with a custom timeout
func (fb *Fastboot) GetVarWithTimeout(name string, timeout time.Duration) (string, error) {
	cmd := fmt.Sprintf("getvar:%s", name)
	_, err := fb.out.Write([]byte(cmd))
	if err != nil {
		return "", fmt.Errorf("fastboot: write failed: %v", err)
	}

	// Read response with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	buf := make([]byte, 256)
	n, err := fb.in.ReadContext(ctx, buf)
	if err != nil {
		return "", fmt.Errorf("fastboot: read failed: %v", err)
	}

	resp := string(buf[:n])
	if strings.HasPrefix(resp, "OKAY") {
		return strings.TrimPrefix(resp, "OKAY"), nil
	} else if strings.HasPrefix(resp, "FAIL") {
		return "", fmt.Errorf("%s", strings.TrimPrefix(resp, "FAIL"))
	} else if strings.HasPrefix(resp, "INFO") {
		// INFO responses are informational, try to read the final OKAY/FAIL
		return fb.readFinalResponse(timeout)
	}
	return resp, nil
}

// readFinalResponse reads until OKAY or FAIL after INFO messages
func (fb *Fastboot) readFinalResponse(timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		buf := make([]byte, 256)
		n, err := fb.in.ReadContext(ctx, buf)
		if err != nil {
			return "", err
		}
		resp := string(buf[:n])
		if strings.HasPrefix(resp, "OKAY") {
			return strings.TrimPrefix(resp, "OKAY"), nil
		} else if strings.HasPrefix(resp, "FAIL") {
			return "", fmt.Errorf("%s", strings.TrimPrefix(resp, "FAIL"))
		}
		// Continue reading if it's another INFO
	}
}

// OemCommand sends an OEM command and returns all INFO responses
func (fb *Fastboot) OemCommand(cmd string) ([]string, error) {
	return fb.OemCommandWithTimeout(cmd, 5*time.Second)
}

// OemCommandWithTimeout sends an OEM command with a custom timeout
func (fb *Fastboot) OemCommandWithTimeout(cmd string, timeout time.Duration) ([]string, error) {
	fullCmd := fmt.Sprintf("oem %s", cmd)
	_, err := fb.out.Write([]byte(fullCmd))
	if err != nil {
		return nil, fmt.Errorf("fastboot: write failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var responses []string
	for {
		buf := make([]byte, 256)
		n, err := fb.in.ReadContext(ctx, buf)
		if err != nil {
			return responses, fmt.Errorf("fastboot: read failed: %v", err)
		}

		resp := string(buf[:n])
		if strings.HasPrefix(resp, "OKAY") {
			return responses, nil
		} else if strings.HasPrefix(resp, "FAIL") {
			errMsg := strings.TrimPrefix(resp, "FAIL")
			if errMsg == "" && len(responses) > 0 {
				// Some commands return error details in INFO
				return responses, fmt.Errorf("command failed")
			}
			return responses, fmt.Errorf("%s", errMsg)
		} else if strings.HasPrefix(resp, "INFO") {
			// Strip "INFO" prefix and collect the response
			info := strings.TrimPrefix(resp, "INFO")
			responses = append(responses, info)
		} else {
			// Unknown response type, include it anyway
			responses = append(responses, resp)
		}
	}
}

// IsFlashing checks if device is busy (unresponsive to getvar)
func (fb *Fastboot) IsFlashing() bool {
	// Use short timeout - if device is flashing, it won't respond quickly
	_, err := fb.GetVarWithTimeout("product", 500*time.Millisecond)
	return err != nil
}

// GetSerial returns the device serial number
func (fb *Fastboot) GetSerial() string {
	return fb.serial
}

// GetSpeed returns the USB connection speed
func (fb *Fastboot) GetSpeed() string {
	return fb.speed
}

// Close releases all resources
func (fb *Fastboot) Close() error {
	if fb.done != nil {
		fb.done()
	}
	if fb.dev != nil {
		fb.dev.Close()
	}
	if fb.ctx != nil {
		fb.ctx.Close()
	}
	return nil
}
