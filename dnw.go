package tensorutils

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/JoshuaDoes/crunchio"
	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

var (
	devicePairsDNW = [][]string{
		{"18D1", "4F00"}, //Google Pixel 6/6a/6Pro
	}
	claimDNW = make(map[string]*DNW)
)

// GetDNW finds the next device known to be compatible with DNW and claims it
func GetDNW() (*DNW, error) {
	closeGhostsDNW()

	//Find a matching device pair available for DNW
	for i := 0; i < len(devicePairsDNW); i++ {
		devPair := devicePairsDNW[i]
		dev := getDevice(devPair[0], devPair[1])
		if dev == nil {
			continue
		}

		//Skip the device if it was already claimed for DNW
		if _, exists := claimDNW[dev.Name]; exists {
			continue
		}

		port, err := serial.Open(dev.Name, &serial.Mode{BaudRate: 115200, Parity: serial.NoParity, DataBits: 8, StopBits: serial.OneStopBit})
		if err != nil {
			return nil, fmt.Errorf("dnw: failed to claim '%s': %v", dev.Name, err)
		}
		port.SetReadTimeout(time.Millisecond * 200)

		//Claim the DNW device
		dnw := new(DNW)
		dnw.port = port
		dnw.info = dev
		claimDNW[dev.Name] = dnw

		//Lock the device mutex until the reader thread is started
		dnw.mutex.Lock()

		//Start the reader thread
		go dnw.readThread()

		return dnw, nil
	}
	return nil, fmt.Errorf("dnw: no device found")
}

func RegisterDevicePairDNW(vid, pid string) {
	found := false
	for _, pair := range devicePairsDNW {
		if pair[0] == vid && pair[1] == pid {
			found = true
			break
		}
	}
	if !found {
		devicePairsDNW = append(devicePairsDNW, []string{vid, pid})
	}
}

func closeGhostsDNW() {
	refreshDevices()
	for port, dnw := range claimDNW {
		found := false
		for i := 0; i < len(knownDevices); i++ {
			if knownDevices[i].Name == port {
				found = true
				break
			}
		}

		if !found {
			dnw.close()
			delete(claimDNW, port)
		}
	}
}

type DNW struct {
	port serial.Port
	info *enumerator.PortDetails

	buffer *crunchio.Buffer //Used for writing the message queue
	reader *crunchio.Buffer //Clone for reading the message queue

	mutex  sync.Mutex
	closed bool
}

func (dnw *DNW) ReadMsg() (*Message, error) {
	dnw.mutex.Lock()
	defer dnw.mutex.Unlock()

	//Read from a clone of the message queue
	return dnw.readMsg(dnw.reader)
}

func (dnw *DNW) readMsg(r *crunchio.Buffer) (*Message, error) {
	if dnw.buffer == nil {
		return nil, fmt.Errorf("dnw: buffer was freed")
	}

	buf := make([]byte, 0)
	for {
		p := make([]byte, 1)
		n, err := dnw.read(r, p)
		if err != nil {
			if len(buf) > 0 {
				return NewMessage(buf), err
			}
			return nil, err
		}
		if n != 1 {
			if len(buf) > 0 {
				//We haven't read a full message yet, but we have some data!
				//Seek backwards and return an empty message so caller can try again on loop
				r.Seek(int64(-1*len(buf)), io.SeekCurrent)
				return nil, nil
			}

			//Nothing was read yet
			if dnw.Closed() {
				break
			}
			continue
		}
		b := p[0]

		if b == '\n' || b == '\r' {
			if len(buf) > 0 {
				break //Parse what we've read so far
			}
			continue //Skip empty queued messages
		}

		buf = append(buf, b)
	}

	if len(buf) > 0 {
		return NewMessage(buf), nil
	}
	return nil, nil
}
func (dnw *DNW) Read(p []byte) (int, error) {
	dnw.mutex.Lock()
	defer dnw.mutex.Unlock()
	if dnw.Closed() {
		return 0, fmt.Errorf("dnw: closed")
	}

	return dnw.read(dnw.reader, p)
}
func (dnw *DNW) read(r *crunchio.Buffer, p []byte) (int, error) {
	return r.Read(p)
}
func (dnw *DNW) readThread() {
	dnw.buffer = crunchio.NewBuffer("EUB Writer", nil)
	dnw.buffer.SetStream(true) //Don't return an EOF when waiting on data
	dnw.reader = dnw.buffer.Reference()
	dnw.reader.SetName("EUB Reader")

	//Unlock the device's mutex to allow I/O operations
	dnw.mutex.Unlock()

	for {
		//Read the next chunk of data
		p := make([]byte, 10240)
		n, err := dnw.port.Read(p)
		if err != nil {
			break
		}
		if n == 0 {
			continue
		}

		//Write it to the message queue
		p = p[:n]
		_, err = dnw.buffer.Write(p)
		if err != nil {
			break
		}
	}

	dnw.close()
}

func (dnw *DNW) WriteCmd(cmd *Command) error {
	if cmd == nil {
		return fmt.Errorf("dnw: nil command")
	}
	return dnw.WriteMsg(NewMessage(cmd.Bytes()))
}
func (dnw *DNW) WriteMsg(msg *Message) error {
	dnw.mutex.Lock()
	defer dnw.mutex.Unlock()
	if dnw.Closed() {
		return fmt.Errorf("dnw: closed")
	}

	p := msg.Bytes()

	/*r := dnw.buffer.Reference()
	defer r.Close()
	r.Seek(0, io.SeekEnd) //Seek to the end of the buffer to only process new responses after writing each block*/

	//Write on loop until the end of message or error
	blockSize := 4096 // Smaller block size for stability
	left := blockSize
	wrote := 0
	maxRetries := 5
	for {
		if dnw.Closed() {
			return fmt.Errorf("dnw: closed but only wrote %d/%d bytes", wrote, len(p))
		}

		/*msg, err := dnw.readMsg(r)
		if err != nil {
			return fmt.Errorf("dnw: failed to read message after writing %d/%d bytes: %v", wrote, len(p), err)
		}
		if msg != nil {
			switch msg.Command() {
			case "C":
				return fmt.Errorf("dnw: %s control received after writing %d/%d bytes", msg.Command(), wrote, len(p))
			case "\x00":
				return fmt.Errorf("dnw: 0x%0X control received after writing %d/%d bytes", msg.Command(), wrote, len(p))
			case "eub":
				switch msg.SubCommand() {
				case "req":
					return fmt.Errorf("dnw: new request received after writing %d/%d bytes", wrote, len(p))
				case "ack":
					return fmt.Errorf("dnw: ack received after writing %d/%d bytes", wrote, len(p))
				case "nak":
					return fmt.Errorf("dnw: nak received after writing %d/%d bytes", wrote, len(p))
				}
			}
			fmt.Printf("dnw: received message after writing %d/%d bytes: %s\n", wrote, len(p), msg.String())
		}*/

		//Keep leftover bytes within msg bounds
		if wrote+left >= len(p) {
			left -= (wrote + left) - len(p)
		}

		// Retry loop for transient errors
		var n int
		var err error
		for retry := 0; retry < maxRetries; retry++ {
			n, err = dnw.write(p[wrote : wrote+left])
			if err == nil {
				break
			}
			errStr := strings.ToLower(err.Error())
			isRetryable := strings.Contains(errStr, "interrupted") ||
				strings.Contains(errStr, "timeout") ||
				strings.Contains(errStr, "resource temporarily unavailable") ||
				strings.Contains(errStr, "eagain") ||
				strings.Contains(errStr, "busy")
			if isRetryable {
				fmt.Printf("dnw: write error at %d/%d bytes, retry %d/%d: %v\n", wrote+n, len(p), retry+1, maxRetries, err)
				time.Sleep(time.Millisecond * 50)
				continue
			}
			break // Non-retryable error
		}
		wrote += n
		if err != nil {
			return fmt.Errorf("dnw: failed to write after %d/%d bytes: %v", wrote, len(p), err)
		}
		if wrote >= len(p) {
			break
		}
	}
	if wrote != len(p) {
		return fmt.Errorf("dnw: only wrote %d/%d bytes", wrote, len(p))
	}

	time.Sleep(time.Millisecond * 1)
	return nil
}
func (dnw *DNW) Write(p []byte) (int, error) {
	dnw.mutex.Lock()
	defer dnw.mutex.Unlock()
	if dnw.Closed() {
		return 0, fmt.Errorf("dnw: closed")
	}

	return dnw.write(p)
}
func (dnw *DNW) write(p []byte) (int, error) {
	n, err := dnw.port.Write(p)
	if err != nil {
		return n, err
	}
	if err := dnw.port.Drain(); err != nil {
		// Drain errors are often transient, only fail on non-interrupted
		if !strings.Contains(err.Error(), "interrupted") {
			return n, err
		}
	}
	return n, nil
}

func (dnw *DNW) Close() error {
	dnw.mutex.Lock()
	defer dnw.mutex.Unlock()
	return dnw.close()
}
func (dnw *DNW) close() error {
	if dnw.Closed() {
		return nil
	}
	if err := dnw.port.Close(); err != nil {
		return err
	}
	dnw.closed = true
	return nil
}
func (dnw *DNW) Closed() bool {
	return dnw.closed
}
func (dnw *DNW) Free() {
	dnw.close()
	dnw.buffer.Reset()
	dnw.buffer = nil
	dnw.reader = nil
	dnw.info = nil
}

func (dnw *DNW) GetBuffer() *crunchio.Buffer {
	if dnw.buffer == nil {
		return nil
	}
	return dnw.buffer.Reference()
}

func (dnw *DNW) GetPort() string {
	if dnw.Closed() {
		return ""
	}
	return dnw.info.Name
}
func (dnw *DNW) GetSerial() string {
	if dnw.Closed() {
		return ""
	}
	return dnw.info.SerialNumber
}
func (dnw *DNW) GetID() string {
	vid := dnw.GetVID()
	pid := dnw.GetPID()
	if vid == "" || pid == "" {
		return ""
	}
	return vid + ":" + pid
}
func (dnw *DNW) GetVID() string {
	if dnw.Closed() {
		return ""
	}
	return dnw.info.VID
}
func (dnw *DNW) GetPID() string {
	if dnw.Closed() {
		return ""
	}
	return dnw.info.PID
}
func (dnw *DNW) GetUSB() bool {
	if dnw.Closed() {
		return false
	}
	return dnw.info.IsUSB
}

// Keepalive reads modem status bits to keep USB connection active
func (dnw *DNW) Keepalive() error {
	if dnw.Closed() {
		return fmt.Errorf("dnw: closed")
	}
	_, err := dnw.port.GetModemStatusBits()
	return err
}
