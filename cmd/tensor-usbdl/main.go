package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/JoshuaDoes/crunchio"
	"github.com/JoshuaDoes/logger"
	tensorutils "github.com/JoshuaDoes/tensor-usbdl"
	"github.com/spf13/pflag"
)

/*
Each bootloader image contains a 4KB (4096 byte) header followed by a code body.

Known fields:
0x00  0    |  512 =    ???: ???
0x200 512  |  512 =    ???: consistent across images, unique per model (or series?)
0x400 1024 |    4 = uint32: magic
0x404 1028 |    8 =    ???: ???
0x40C 1036 |    4 = uint32: length of bootloader body
0x410 1040 |    4 = uint32: "USB Bootable" bit amongst other bitflags?
0x414 1044 |   12 =    ???: ???
0x420 1056 |   32 =  bytes: signature 1?
0x440 1088 |   32 =  bytes: signature 2?
0x460 1120 | 2976 =    ???: ??? (always empty)
*/

/* TODO:
FBPK:
- Create FBPK package, migrate main of fbpk to unique cmd
- Parse and use FBPKv2 bootloader image via fbpk

OTA:
- Include aota
- Parse and use OTA payload image via aota

DNW:
- Create DNW package, create main for unique cmd
- Create reader and writer threads with read and write queues
- Add cloning support for unique position trackers to allow independent queue seeking
- Create Go types and enums for known fields in a response message to clean up processing
- Support waiting for a queued message with constraints (i.e. ACK/NAK for EUB)
*/

const (
	app = "Tensor-USBDL"
	ver = "v0.1.0"
	dev = "JoshuaDoes"
)

var (
	bootloaders map[string]*crunchio.Buffer = make(map[string]*crunchio.Buffer)
)

var (
	help         = false
	useDNW       = false
	bitUSB       = false
	fuzzDPM      = false
	stop         = false
	testFastboot = false
	fastbootSerial = ""

	src         = "sources"
	factory     = "bootloader.img"
	ota         = "payload.bin"
	ufs         = "ufs.img"
	partition0  = "partition_0.img"
	partition1  = "partition_1.img"
	partition2  = "partition_2.img"
	partition3  = "partition_3.img"
	bl1         = "bl1.img"
	dpm         = ""
	pbl         = "pbl.img"
	bl2         = "bl2.img"
	gcf         = "gcf.img"
	gsa         = "gsa.img"
	gsaf        = "gsaf.img"
	abl         = "abl.img"
	tzsw        = "tzsw.img"
	ldfw        = "ldfw.img"
	bl31        = "bl31.img"
	ufsfwupdate = "ufsfwupdate.img"

	address = tensorutils.OpDNW
	crc     = []byte{0xFF, 0xFF}
	header  = int64(4096)

	log *logger.Logger
)

func usage() {
	prog := strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0]))
	text := fmt.Sprintf(
		" Tensor USB Downloader is a tool to send bootloaders over serial USB to a"+
			" connected Google Pixel device in Exynos USB Boot mode."+
			"\n"+
			" By default, we look for all specified images in a relative folder named '%s'.\n"+
			" If available, the factory bootloader image will be used first, and defaults to '%s'.\n"+
			" In lieu of that, an OTA payload may be used instead, defaulting to '%s'.\n"+
			" Lastly, we try to discover the individual images, named to match their counterparts (as available in"+
			" both a factory images ZIP's embedded images ZIP as well as an OTA payload).\n"+
			"\n"+
			" When specifying a factory bootloader image, you must provide the path to either the raw image itself"+
			" or a factory images ZIP containing the bootloader image.\n"+
			" When specifying an OTA payload, you must provide the path to either the payload image itself or an OTA"+
			" ZIP containing the payload image.\n"+
			"\n"+
			" Usage of %s:\n"+
			" -h, --help    | none   | Prints the help you see now and ignores other arguments\n"+
			"\n"+
			" > Sources\n"+
			" -i, --src     | string | Directory with bootloader images to serve         | %s\n"+
			" -f, --factory | string | FBPK (FastBoot PacK) v2 bootloader image to serve | %s\n"+
			" -o, --ota     | string | OTA payload to serve                              | %s\n"+
			" -u, --ufs     | string | UFS image to serve                                | %s\n"+
			" --partition0  | string | 1st UFS LUN to serve                              | %s\n"+
			" --partition1  | string | 2nd UFS LUN to serve                              | %s\n"+
			" --partition2  | string | 3rd UFS LUN to serve                              | %s\n"+
			" --partition3  | string | 4th UFS LUN to serve                              | %s\n"+
			" -1, --bl1     | string | BL1 image to serve                                | %s\n"+
			" -p, --pbl     | string | PBL image to serve                                | %s\n"+
			" -2, --bl2     | string | BL2 image to serve                                | %s\n"+
			" -a, --abl     | string | ABL image to serve                                | %s\n"+
			" -3, --bl31    | string | BL31 image to serve                               | %s\n"+
			" -F, --gcf     | string | GCF image to serve                                | %s\n"+
			" -g, --gsa     | string | GSA image to serve                                | %s\n"+
			" -G, --gsaf    | string | GSAF image to serve                               | %s\n"+
			" -t, --tzsw    | string | TZSW (TrustZone SoftWare) image to serve          | %s\n"+
			" -l, --ldfw    | string | LDFW (LoaDable FirmWare) image to serve           | %s\n"+
			" --ufsfwupdate | string | UFS firmware update image to serve                | %s\n"+
			" -d, --dpm     | string | DPM image to serve instead of zeroed 12KB\n"+
			"\n"+
			" > Controls\n"+
			" --address     | hex    | Target download address (or command) to write to             | %X\n"+
			" --header      | number | Number of bytes to interpret as header for splittable images | %d\n"+
			" -c, --crc     | hex    | Overrides the calculated CRC when writing DNW messages\n"+
			" --dnw         | none   | Overrides the download address (or command) to %X\n"+
			" --usb         | none   | Sets the 1040th byte to 01 if it is 00\n"+
			" --fuzzdpm     | none   | (DANGEROUS!) Fuzzes an empty DPM image with random data\n"+
			" --stop        | none   | Sends the DNW STOP command to the device upon connection\n"+
			" --fastboot    | none   | Skip EUB and monitor fastboot device directly\n"+
			" --serial      | string | Specify fastboot device serial (auto-detects if omitted)\n",
		src, factory, ota,
		prog,
		src, factory, ota,
		ufs, partition0, partition1, partition2, partition3,
		bl1, pbl, bl2, abl, bl31, gcf, gsa, gsaf, tzsw, ldfw, ufsfwupdate,
		address, header, tensorutils.OpDNW)
	fmt.Fprintf(os.Stderr, "%s\n", text)
}

func checkRootPrivileges() {
	if runtime.GOOS == "linux" && os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "Error: This program requires root privileges on Linux.")
		fmt.Fprintln(os.Stderr, "Please run with: sudo ./tensor-usbdl [options]")
		os.Exit(1)
	}
}

func main() {
	fmt.Printf("%s %s - %s\n", app, ver, dev)
	checkRootPrivileges()

	pflag.Usage = usage
	pflag.CommandLine.SortFlags = false
	pflag.BoolVarP(&help, "help", "h", false, "")
	pflag.BoolVar(&useDNW, "dnw", false, "")
	pflag.BoolVar(&bitUSB, "usb", false, "")
	pflag.BoolVar(&fuzzDPM, "fuzzdpm", false, "")
	pflag.BoolVar(&stop, "stop", false, "")
	pflag.BoolVar(&testFastboot, "fastboot", false, "")
	pflag.StringVar(&fastbootSerial, "serial", "", "")
	pflag.StringVarP(&src, "src", "i", src, "")
	pflag.StringVarP(&factory, "factory", "f", factory, "")
	pflag.StringVarP(&ota, "ota", "o", ota, "")
	pflag.StringVarP(&ufs, "ufs", "u", ufs, "")
	pflag.StringVar(&partition0, "partition0", partition0, "")
	pflag.StringVar(&partition1, "partition1", partition1, "")
	pflag.StringVar(&partition2, "partition2", partition2, "")
	pflag.StringVar(&partition3, "partition3", partition3, "")
	pflag.StringVarP(&bl1, "bl1", "1", bl1, "")
	pflag.StringVarP(&pbl, "pbl", "p", pbl, "")
	pflag.StringVarP(&bl2, "bl2", "2", bl2, "")
	pflag.StringVarP(&abl, "abl", "a", abl, "")
	pflag.StringVarP(&bl31, "bl31", "3", bl31, "")
	pflag.StringVarP(&gcf, "gcf", "F", gcf, "")
	pflag.StringVarP(&gsa, "gsa", "g", gsa, "")
	pflag.StringVarP(&gsaf, "gsaf", "G", gsaf, "")
	pflag.StringVarP(&tzsw, "tzsw", "t", tzsw, "")
	pflag.StringVarP(&ldfw, "ldfw", "l", ldfw, "")
	pflag.StringVar(&ufsfwupdate, "ufsfwupdate", ufsfwupdate, "")
	pflag.StringVarP(&dpm, "dpm", "d", dpm, "")
	pflag.BytesHexVar(&address, "address", address, "")
	pflag.Int64Var(&header, "header", header, "")
	pflag.BytesHexVarP(&crc, "crc", "c", crc, "")
	pflag.Parse()

	if help {
		usage()
		return
	}

	log = logger.NewLogger(app, 2)

	// If --fastboot is specified, skip EUB and go directly to fastboot monitoring
	// After fastboot monitoring returns, fall through to normal EUB flow
	if testFastboot {
		if fastbootSerial != "" {
			log.Infof("Testing fastboot mode with serial: %s", fastbootSerial)
		} else {
			log.Infoln("Testing fastboot mode (auto-detecting device)")
		}
		monitorFastboot(log, fastbootSerial, true) // true = wait for device
		log.Infoln("Fastboot monitoring complete, continuing to EUB mode...")
	}

	if header <= 0 {
		log.Errorln("[!] Header size must be positive number!")
		return
	}

	if useDNW && len(address) == 0 {
		address = tensorutils.OpDNW
	}

	if src == "" {
		src = "sources"
	}
	if err := isDir(src); err != nil {
		log.Errorf("Error opening directory '%s': %v", src, err)
		return
	}

	/*if err := isFile(src, factory); err == nil {
		fmt.Println("[*] Processing FBPKv2")
	} else if err := isFile(src, ota); err == nil {
		fmt.Println("[*] Processing OTA")
	} else {
		fmt.Println("[*] Processing raw")
	}*/

	//TODO: Actually use the FBPKv2 or OTA when specified

	//----------------------
	// Load bootloaders into memory

	var img []byte
	var err error

	img = make([]byte, 4096)
	if dpm != "" {
		img, err = readFile(dpm)
		if err != nil {
			log.Errorf("Error reading DPM image: %v", err)
			return
		}
	}
	bootloaders["DPM"] = crunchio.NewBuffer(dpm, img)

	img, err = readFile(bl1)
	if err != nil {
		log.Errorf("Error reading BL1 image: %v", err)
		return
	}
	bootloaders["BL1"] = crunchio.NewBuffer(bl1, img)

	img, err = readFile(pbl)
	if err != nil {
		log.Errorf("Error reading PBL image: %v", err)
		return
	}
	bootloaders["PBL"] = crunchio.NewBuffer(pbl, img)

	img, err = readFile(bl2)
	if err != nil {
		log.Errorf("Error reading BL2 image: %v", err)
		return
	}
	bootloaders["BL2"] = crunchio.NewBuffer(bl2, img)

	img, err = readFile(gsa)
	if err != nil {
		log.Errorf("Error reading GSA image: %v", err)
		return
	}
	bootloaders["GSA"] = crunchio.NewBuffer(gsa, img)

	img, err = readFile(abl)
	if err != nil {
		log.Errorf("Error reading ABL image: %v", err)
		return
	}
	bootloaders["ABL"] = crunchio.NewBuffer(abl, img)

	img, err = readFile(tzsw)
	if err != nil {
		log.Errorf("Error reading TZSW image: %v", err)
		return
	}
	bootloaders["TZSW"] = crunchio.NewBuffer(tzsw, img)

	img, err = readFile(ldfw)
	if err != nil {
		log.Errorf("Error reading LDFW image: %v", err)
		return
	}
	bootloaders["LDFW"] = crunchio.NewBuffer(ldfw, img)

	img, err = readFile(bl31)
	if err != nil {
		log.Errorf("Error reading BL31 image: %v", err)
		return
	}
	bootloaders["BL31"] = crunchio.NewBuffer(bl31, img)

	//Bootloaders that may not be needed depending on the device
	img, err = readFile(gcf)
	if err == nil {
		bootloaders["GCF"] = crunchio.NewBuffer(gcf, img)
	}
	img, err = readFile(gsaf)
	if err == nil {
		bootloaders["GSAF"] = crunchio.NewBuffer(gsaf, img)
	}

	//----------------------

	lastSent := ""
	for {
		var dnw *tensorutils.DNW
		var timeStart time.Time
		var lastTrace []string

		fmt.Println("")
		log.Infoln("Scanning for device...")
		for {
			dnw, err = tensorutils.GetDNW()
			if err == nil {
				break
			}
		}
		timeStart = time.Now()

		log.Infoln("Connected to device!")
		log.Traceln("- Port:  ", dnw.GetPort())
		log.Traceln("- ID:    ", dnw.GetID())
		log.Traceln("- Serial:", dnw.GetSerial())
		log.Traceln("- USB:   ", dnw.GetUSB())

		//Send a newline character to make sure the device sends us the first message
		dnw.Write([]byte{'\n'})

		if stop {
			log.Infoln("Sending stop command unconditionally")
			dnw.WriteCmd(tensorutils.CmdStop)
		}

		request := ""
		upload := false
		bl3bSent := false
		var bl3bSentTime time.Time
		bl3bWarningShown := false

		// Start background keepalive goroutine with countdown
		keepaliveDone := make(chan struct{})
		go func() {
			ticker := time.NewTicker(60 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-keepaliveDone:
					return
				case <-ticker.C:
					if dnw.Closed() {
						continue
					}
					dnw.Keepalive()

					if bl3bSent {
						elapsed := time.Since(bl3bSentTime)
						remaining := 30*time.Minute - elapsed
						if remaining > 0 {
							mins := int(remaining.Minutes()) + 1 // Round up
							log.Infof("Estimated time to fastboot: ~%d minutes", mins)
						} else {
							log.Infoln("Device expected to reboot to fastboot soon...")
						}
					} else {
						elapsed := time.Since(timeStart)
						if elapsed > 5*time.Minute && !bl3bWarningShown {
							log.Warnln("BL3B not reached after 5 minutes - consider restarting the process")
							bl3bWarningShown = true
						}
					}
				}
			}
		}()

		for {
			if dnw.Closed() {
				break
			}

			var msg *tensorutils.Message
			msg, err = dnw.ReadMsg()
			if err != nil {
				if msg != nil {
					log.Debugln("Last message from device:", msg)
				}
				log.Errorln("Error reading message:", err)
				err = nil //Don't reprint the error later
				break
			}
			if msg == nil {
				continue
			}

			switch msg.Command() {
			case "C":
				if !upload {
					log.Traceln("Not allowed to upload right now")
					continue
				}
				upload = false
				log.Infof("> %s", request)

				var bl *crunchio.Buffer
				var op int //0=full, 1=header, 2=body

				switch request { //Cases ordered by requests on Pixel 7 series
				case "BL1":
					bl = bootloaders["BL1"]
					op = 0
				case "DPM":
					bl = bootloaders["DPM"]
					op = 0
				case "EPBL":
					bl = bootloaders["PBL"]
					op = 0
				case "BL2":
					bl = bootloaders["BL2"]
					op = 1
				case "BL2B":
					bl = bootloaders["BL2"]
					op = 2
				case "GSA1":
					bl = bootloaders["GSA"]
					op = 0
				case "ABL":
					bl = bootloaders["ABL"]
					op = 1
				case "ABLB":
					bl = bootloaders["ABL"]
					op = 2
				case "TZSW":
					bl = bootloaders["TZSW"]
					op = 1
				case "TZSB":
					bl = bootloaders["TZSW"]
					op = 2
				case "LDFW":
					bl = bootloaders["LDFW"]
					op = 1
				case "LDFB":
					bl = bootloaders["LDFW"]
					op = 2
				case "BL31":
					bl = bootloaders["BL31"]
					op = 1
				case "BL3B":
					bl = bootloaders["BL31"]
					op = 2
				case "GCF":
					bl = bootloaders["GCF"]
					op = 1
				case "GCFB":
					bl = bootloaders["GCF"]
					op = 2
				case "GSAF":
					bl = bootloaders["GSAF"]
					op = 0
				}

				if bl == nil {
					err = fmt.Errorf("unknown image requested: %s", request)
				} else {
					size := int64(bl.Size())
					switch op {
					case 0:
						err = writeRaw(dnw, address, nil, bl.Bytes())
					case 1:
						err = writeRaw(dnw, address, nil, bl.Buffer().ReadBytes(0, header))
					case 2:
						err = writeRaw(dnw, address, nil, bl.Buffer().ReadBytes(header, size-header))
					}
				}
				if err == nil {
					log.Infof("Sent %s", request)
					lastSent = request
				}
			case "\x00":
				log.Tracef("Received control: 0x%0X", msg.Command())
			case "exynos_usb_booting":
				log.Debugln("Device identified as", msg.Device())
			case "eub":
				bootloader := strings.ToUpper(msg.Argument())
				switch msg.SubCommand() {
				case "req":
					if request == bootloader {
						log.Traceln("Received duplicate bootloader request")
						continue
					}
					log.Infoln("Requested", bootloader)
					request = bootloader
					upload = true
				case "ack":
					log.Debugln("Acknowledged", bootloader)
					if bootloader == "BL3B" && !bl3bSent {
						bl3bSent = true
						bl3bSentTime = time.Now()
						log.Infoln("BL3B sent - waiting for battery charge and fastboot (typically 30 minutes, may take longer)...")
					}
					upload = true
				case "nak":
					log.Errorln("Refused", bootloader)
				default:
					err = fmt.Errorf("unknown EUB message: %s", msg)
				}
			case "irom_booting_failure":
				trace := strings.Split(msg.Device(), "\x00")
				trace = trace[1:16] //Remove the empty prefix and suffix
				if lastTrace != nil {
					diff := false
					for i := 0; i < len(trace); i++ {
						if trace[i] != lastTrace[i] {
							diff = true
							break
						}
					}
					if !diff {
						log.Traceln("Received duplicate failure trace")
						continue
					}
				}
				lastTrace = trace

				brErr := "BootROM error booting"
				if lastSent != "" {
					brErr += " " + lastSent
				}
				brErr += ":"
				for i := 0; i < len(trace); i++ {
					brErr += fmt.Sprintf("\n> %s", trace[i])
				}
				err = fmt.Errorf("%s", brErr)
			case "error":
				err = fmt.Errorf("%s: %s", msg.SubCommand(), msg.Argument())
			default:
				err = fmt.Errorf("unhandled message: 0x%0X (%s)", msg, msg)
			}

			if err != nil {
				log.Errorf("Internal error: %v", err)
			}
		}

		// Stop keepalive goroutine
		close(keepaliveDone)

		// Store serial before closing
		deviceSerial := dnw.GetSerial()

		if dnw.Closed() {
			log.Infoln("Device disconnected!")
		} else {
			log.Infoln("Disconnecting from device...")
			if err := dnw.Close(); err != nil {
				log.Errorln("Error closing connection:", err)
			}
		}

		/*buf := dnw.GetBuffer()
		log.Tracef("Packet dump of messages:\n%s", buf.String())
		log.Tracef("Packet dump of messages as hex:\n0x%0X", buf.Bytes())*/
		dnw.Free()

		log.Traceln("Connection lasted", time.Since(timeStart).String())

		// Monitor fastboot if BL3B was sent
		if bl3bSent {
			// First, scan for EUB reconnection for 30 seconds
			log.Infoln("Scanning for EUB reconnection (30 seconds)...")
			eubFound := false
			for i := 0; i < 10; i++ { // 10 checks * 3 seconds = 30 seconds
				time.Sleep(3 * time.Second)
				testDnw, err := tensorutils.GetDNW()
				if err == nil {
					log.Infoln("Device reconnected to EUB mode")
					testDnw.Close()
					eubFound = true
					break
				}
			}

			// If no EUB reconnection, switch to fastboot scanning
			if !eubFound {
				monitorFastboot(log, deviceSerial, false)
			}
		}
	}
}

// monitorFastboot polls the device in fastboot mode for battery and unlock status
func monitorFastboot(log *logger.Logger, serial string, waitFirst bool) {
	if waitFirst {
		log.Infoln("Waiting for device to enter fastboot (up to 10 minutes)...")
	}

	// Wait up to 10 minutes for fastboot device to appear, checking every 30 seconds
	var fb *tensorutils.Fastboot
	var err error
	maxWait := 10 * time.Minute
	checkInterval := 30 * time.Second
	waited := time.Duration(0)

	for waited < maxWait {
		fb, err = tensorutils.GetFastboot(serial)
		if err == nil {
			break // Found device
		}
		time.Sleep(checkInterval)
		waited += checkInterval
	}

	if fb == nil {
		log.Infoln("Device not found in fastboot mode after 10 minutes - returning to EUB scan")
		return
	}

	log.Infoln("Device found in fastboot mode")

	firstConnection := true
	for {
		// Check if flashing is in progress (device unresponsive)
		if fb.IsFlashing() {
			log.Infoln("Flashing detected - exiting to allow flash to complete")
			fb.Close()
			return
		}

		// On first connection, display full device info
		isFirstConnection := firstConnection
		if firstConnection {
			log.Infoln("=== Fastboot Device Info ===")
			displayFastbootInfo(log, fb)
			firstConnection = false
		}

		// Get battery voltage and current
		voltage, vErr := fb.GetVar("battery-voltage")
		current, cErr := fb.GetVar("battery-current")

		if vErr != nil {
			log.Tracef("Failed to get battery voltage: %v", vErr)
		}
		if cErr != nil {
			log.Tracef("Failed to get battery current: %v", cErr)
		}
		if vErr == nil || cErr == nil {
			checkBatteryStatus(log, voltage, current, isFirstConnection)
		}

		// Check if charging has stopped (battery full or not charging)
		// Small negative values like -7mA also indicate charging stopped
		if cErr == nil {
			mA := parseCurrentMA(current)
			if mA > -15 && mA < 50 {
				// Check device state - if not "error", unbricking worked
				deviceState, sErr := fb.GetVar("device-state")
				if sErr == nil && deviceState != "error" {
					log.Infoln("Charging complete - device state is not error, unbricking successful!")

					// Check if already unlocked
					unlocked, uErr := fb.GetVar("unlocked")
					if uErr == nil && unlocked == "yes" {
						log.Infoln("Device is already unlocked - you can flash normally")
					} else {
						log.Infoln("Attempting to send unlock command...")
						// Try flashing unlock first (Pixel standard), then oem unlock
						if err := fb.FlashingUnlock(); err != nil {
							log.Warnf("flashing unlock failed, trying oem unlock: %v", err)
							if _, err := fb.OemCommand("unlock"); err != nil {
								log.Warnf("oem unlock also failed (may need manual confirmation): %v", err)
							}
						}
						log.Infoln("You can now flash your device normally")
					}
					fb.Close()
					return
				}

				log.Infoln("Charging complete or stopped - powering off device to return to EUB mode")
				// Try multiple shutdown methods
				// Note: powerdown causes device to hang, skip it
				// log.Infoln("Trying powerdown...")
				// fb.PowerOff()
				// log.Infoln("Waiting for device to power off...")
				// time.Sleep(3 * time.Second)

				// Try reboot first
				log.Infoln("Trying reboot...")
				if err := fb.Reboot(); err != nil {
					log.Warnf("Reboot command failed: %v", err)
				}
				time.Sleep(3 * time.Second)

				// Check if device is still connected
				log.Infoln("Checking if device disconnected...")
				time.Sleep(1 * time.Second)
				if _, err := fb.GetVarWithTimeout("product", 1*time.Second); err == nil {
					fb.Close()
					log.Warnln("Auto power-off failed. Please manually:")
					log.Warnln("  1. Disconnect the phone from USB")
					log.Warnln("  2. Hold power button to power off the device")
					log.Warnln("  3. Reconnect the phone to USB")
					log.Infoln("Waiting for device to disconnect...")
					for {
						time.Sleep(1 * time.Minute)
						testFb, err := tensorutils.GetFastboot(serial)
						if err != nil {
							break // Device disconnected
						}
						// Show battery status while waiting
						voltage, _ := testFb.GetVar("battery-voltage")
						current, _ := testFb.GetVar("battery-current")
						checkBatteryStatus(log, voltage, current, false)
						testFb.Close()
					}
					log.Infoln("Device disconnected, continuing to EUB mode...")
				} else {
					fb.Close()
				}
				return
			}
		}

		fb.Close()
		time.Sleep(1 * time.Minute)

		// Reconnect for next check
		fb, err = tensorutils.GetFastboot(serial)
		if err != nil {
			log.Infoln("Device disconnected from fastboot - returning to EUB scan")
			return
		}
	}
}

// displayFastbootInfo queries and displays device information from fastboot
func displayFastbootInfo(log *logger.Logger, fb *tensorutils.Fastboot) {
	// Device identification
	if val, err := fb.GetVar("product"); err == nil {
		log.Infof("Product: %s", val)
	}
	if val, err := fb.GetVar("hw-revision"); err == nil {
		log.Infof("Product revision: %s", val)
	}
	if val, err := fb.GetVar("version-bootloader"); err == nil {
		log.Infof("Bootloader version: %s", val)
	}
	if val, err := fb.GetVar("version-baseband"); err == nil {
		log.Infof("Baseband version: %s", val)
	}
	if val, err := fb.GetVar("serialno"); err == nil {
		log.Infof("Serial number: %s", val)
	}
	if speed := fb.GetSpeed(); speed != "" {
		log.Infof("USB speed: %s", formatUSBSpeed(speed))
	}

	// Security state
	if val, err := fb.GetVar("secure"); err == nil {
		log.Infof("Secure boot: %s", val)
	}
	if val, err := fb.GetVar("nos-production"); err == nil {
		log.Infof("NOS production: %s", val)
	}
	if val, err := fb.GetVar("unlocked"); err == nil {
		log.Infof("Device state: %s", val)
	}

	// GSC (Titan M) version
	if responses, err := fb.OemCommand("gsc version"); err == nil {
		for _, line := range responses {
			log.Infof("GSC: %s", strings.TrimSpace(line))
		}
	} else {
		log.Tracef("GSC version: %v", err)
	}

	// Hardware info
	ddrManu, _ := fb.GetVar("ddr-manu")
	ddrSize, _ := fb.GetVar("ddr-size")
	ddrType, _ := fb.GetVar("ddr-type")
	if ddrManu != "" || ddrSize != "" || ddrType != "" {
		log.Infof("DRAM: %s %s %s", ddrManu, ddrType, ddrSize)
	}
	if val, err := fb.GetVar("ufs-manufacturer"); err == nil {
		log.Infof("UFS: %s", val)
	}

	// Boot info
	if val, err := fb.GetVar("current-slot"); err == nil {
		log.Infof("Boot slot: %s", val)
	}
	if val, err := fb.GetVar("reason"); err == nil {
		log.Infof("Enter reason: %s", val)
	}
	if val, err := fb.GetVar("uart"); err == nil {
		log.Infof("UART: %s", val)
	}

	// Battery info
	if val, err := fb.GetVar("battery-voltage"); err == nil {
		mV := parseVoltage(val)
		log.Infof("Battery voltage: %smV", formatWithComma(mV))
	}
	if val, err := fb.GetVar("battery-current"); err == nil {
		mA := parseCurrentMA(val)
		log.Infof("Battery current: %smA", formatWithComma(mA))
	}

	log.Infoln("============================")
}

// checkBatteryStatus displays combined battery voltage and current status
func checkBatteryStatus(log *logger.Logger, voltage string, current string, isFirstConnection bool) {
	mV := parseVoltage(voltage)
	mA := parseCurrentMA(current)

	fmtV := formatWithComma(mV)
	fmtA := formatWithComma(mA)

	// Determine status message based on voltage and current
	if mA < 0 {
		// Discharging - device is not charging properly
		log.Warnf("Battery: %smV @ %smA - DISCHARGING!", fmtV, fmtA)
		if isFirstConnection {
			log.Warnf(">>> UNPLUG AND REPLUG the USB cable to restart charging <<<")
		}
	} else if mV < 4200 {
		// Need more charge
		if mA < 600 {
			log.Warnf("Battery: %smV @ %smA - need 4,200mV, charging slowly, try USB-C port", fmtV, fmtA)
		} else {
			log.Warnf("Battery: %smV @ %smA - need at least 4,200mV for stable boot", fmtV, fmtA)
		}
	} else if mA < 500 {
		// Sufficient but charging slowly
		log.Infof("Battery: %smV @ %smA - charging slowly, full charge may take up to 24 hours", fmtV, fmtA)
	} else {
		// Charging well
		log.Infof("Battery: %smV @ %smA - charging well, keep waiting for full charge", fmtV, fmtA)
	}
}

// formatWithComma formats an integer with comma separators
func formatWithComma(n int) string {
	// Handle negative numbers
	if n < 0 {
		return "-" + formatWithComma(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	// Insert comma for thousands
	return s[:len(s)-3] + "," + s[len(s)-3:]
}

// parseCurrentMA extracts the current value in mA from a string like "319 mA" or "-106mA"
func parseCurrentMA(current string) int {
	current = strings.TrimSpace(current)
	current = strings.TrimSuffix(current, " mA")
	current = strings.TrimSuffix(current, "mA")
	var mA int
	fmt.Sscanf(current, "%d", &mA)
	return mA
}

// parseVoltage extracts the voltage value in mV from a string like "4302 mV" or "4302mV"
func parseVoltage(voltage string) int {
	voltage = strings.TrimSpace(voltage)
	voltage = strings.TrimSuffix(voltage, " mV")
	voltage = strings.TrimSuffix(voltage, "mV")
	var mV int
	fmt.Sscanf(voltage, "%d", &mV)
	return mV
}

// formatUSBSpeed converts gousb speed string to human-readable format with max charge rate
func formatUSBSpeed(speed string) string {
	switch strings.ToLower(speed) {
	case "low":
		return "USB 1.0 Low Speed (1.5 Mbps, max 100mA)"
	case "full":
		return "USB 1.1 Full Speed (12 Mbps, max 100mA)"
	case "high":
		return "USB 2.0 High Speed (480 Mbps, max 500mA)"
	case "super":
		return "USB 3.0 SuperSpeed (5 Gbps, max 900mA)"
	case "superplus":
		return "USB 3.1 SuperSpeed+ (10 Gbps, max 900mA)"
	case "superspeedplus":
		return "USB 3.1 SuperSpeed+ (10 Gbps, max 900mA)"
	default:
		return speed
	}
}
