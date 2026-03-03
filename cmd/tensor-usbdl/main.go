package main

import (
	"fmt"
	"os"
	"path/filepath"
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
	ver = "v0.2.0"
	dev = "JoshuaDoes"
)

var (
	bootloaders map[string]*crunchio.Buffer = make(map[string]*crunchio.Buffer)
)

var (
	help    = false
	useDNW  = false
	bitUSB  = false
	fuzzDPM = false
	stop    = false

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
			" --stop        | none   | Sends the DNW STOP command to the device upon connection\n",
		src, factory, ota,
		prog,
		src, factory, ota,
		ufs, partition0, partition1, partition2, partition3,
		bl1, pbl, bl2, abl, bl31, gcf, gsa, gsaf, tzsw, ldfw, ufsfwupdate,
		address, header, tensorutils.OpDNW)
	fmt.Fprintf(os.Stderr, "%s\n", text)
}

func main() {
	fmt.Printf("%s %s - %s\n", app, ver, dev)

	pflag.Usage = usage
	pflag.CommandLine.SortFlags = false
	pflag.BoolVarP(&help, "help", "h", false, "")
	pflag.BoolVar(&useDNW, "dnw", false, "")
	pflag.BoolVar(&bitUSB, "usb", false, "")
	pflag.BoolVar(&fuzzDPM, "fuzzdpm", false, "")
	pflag.BoolVar(&stop, "stop", false, "")
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
		device := Identify(dnw.GetID())

		//Send a newline character to make sure the device sends us the first message
		dnw.Write([]byte{'\n'})

		if stop {
			log.Infoln("Sending stop command unconditionally")
			dnw.WriteCmd(tensorutils.CmdStop)
		}

		request := ""
		upload := false
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
					if device.Brand == "Google" && device.Model == "Pixel 8 series" {
						op = 1
					} else {
						op = 0
					}
				case "EPBB":
					bl = bootloaders["PBL"]
					op = 2
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
				device = Identify(msg.Device())
				log.Debugln("Device identified as", msg.Device())
				log.Infoln("Device detected:", device.String())
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
	}
}
