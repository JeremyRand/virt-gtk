// Copyright 2024-2026 Jeremy Rand.
//
// This file is part of virt-gtk.
//
// virt-gtk is free software: you can redistribute it and/or
// modify it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// virt-gtk is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with virt-gtk.  If not, see
// <https://www.gnu.org/licenses/>.

package main

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"log"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/digitalocean/go-libvirt"
	"github.com/mattn/go-shellwords"

	"github.com/JeremyRand/virt-gtk/common"
)

// TODO: find a way to make KeePassXC auto-type work with Wayland

// TODO: to run under X11, you need to do this:
// xhost +SI:localuser:root
// Or a different user than root
// To undo:
// xhost -SI:localuser:root

var (
	displayServer = flag.String("display-server", "wayland", "x11 or wayland")
	runAsRoot = flag.Bool("root", false, "run as root")
	qemuUsername = flag.String("qemu-user", "libvirt-qemu", "user to run Qemu as (if not root)")
	vmName = flag.String("vm", "", "name of VM to launch")
	verbose = flag.Bool("verbose", false, "verbose output")
)

type fsPath struct {
	dir      string
	readOnly bool
	caching  string
}

func fsPaths(xmlDesc string) []fsPath {
	type Driver struct {
		DType string `xml:"type,attr"`
	}

	type Source struct {
		Dir string `xml:"dir,attr"`
	}

	type ReadOnly struct {
	}

	// Example XML: https://libvirt.org/kbase/virtiofs.html#id4
	type Cache struct {
		Mode string `xml:"mode,attr"`
	}

	type Filesystem struct {
		Driver Driver `xml:"driver"`
		Source Source `xml:"source"`
		ReadOnly *ReadOnly `xml:"readonly"`
		Cache Cache `xml:"binary>cache"`
	}

	type Result struct {
		Filesystems []Filesystem `xml:"devices>filesystem"`
	}

	result := Result{}

	err := xml.Unmarshal([]byte(xmlDesc), &result)

	if err != nil {
		log.Fatalf("failed to parse filesystems from XML: %v", err)
	}

	paths := []fsPath{}

	for _, fs := range result.Filesystems {
		if fs.Driver.DType == "virtiofs" {
			path := fsPath{
				dir:      fs.Source.Dir,
				readOnly: fs.ReadOnly != nil,
				caching:  fs.Cache.Mode,
			}

			if path.caching == "" {
				path.caching = "auto"
			}

			paths = append(paths, path)
		}
	}

	return paths
}

func runHelperCmd(cmd *exec.Cmd) {
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	if err != nil {
		log.Fatalf("failed to run helper process: %v, %s", err, out.String())
	}
}

func killHelperCmd(cmd *exec.Cmd) {
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Process.Kill()
	if err != nil {
		log.Fatalf("failed to kill helper process: %v, %s", err, out.String())
	}
}

func main() {
	flag.Parse()

	if *displayServer != "x11" && *displayServer != "wayland" {
		log.Fatalf("-display-server arg must be x11 or wayland")
	}
	if *vmName == "" {
		log.Fatalf("-vm arg is required")
	}

	uri, _ := url.Parse(string(libvirt.QEMUSystem))
	l, err := libvirt.ConnectToURI(uri)
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}

	defer l.Disconnect()

	domain, err := l.DomainLookupByName(*vmName)
	if err != nil {
		log.Fatalf("failed to lookup domain: %v", err)
	}

	xmlDesc, err := l.DomainGetXMLDesc(domain, libvirt.DomainXMLSecure | libvirt.DomainXMLInactive)
	if err != nil {
		log.Fatalf("failed to get XML: %v", err)
	}

	nativeShell, err := l.ConnectDomainXMLToNative("qemu-argv", xmlDesc, 0)
	if err != nil {
		log.Fatalf("failed to get QEMU native shell: %v", err)
	}

	_, args, err := shellwords.ParseWithEnvs(nativeShell)
	if err != nil {
		log.Fatalf("failed to parse QEMU args: %v", err)
	}

	newArgs := []string{}

	if !strings.HasPrefix(args[0], "/usr/bin/") {
		log.Fatalf("Expected /usr/bin/ prefix not found: %s", args[0])
	}

	virtiofsdCmds := []*exec.Cmd{}
	virtiofsdSockets := []string{}
	virtiofsdDir, err := os.MkdirTemp("", "virt-gtk-virtiofsd")
	if err != nil {
		log.Fatalf("failed to create virtiofsd socket directory: %v", err)
	}
	defer os.RemoveAll(virtiofsdDir) // clean up

	// Handle /usr/bin/qemu-system-*
	qemuExe := args[0]
	args = args[1:]

	// Handle other args
	for len(args) > 0 {
		// Use SDL audio instead of SPICE
		if args[0] == "-audiodev" {
			newArgs = append(newArgs, args[0])
			args = args[1:]

			audiodev := map[string]any{}
			err = json.Unmarshal([]byte(args[0]), &audiodev)
			if err != nil {
				log.Fatalf("failed to parse audiodev: %v", err)
			}

			if audiodev["driver"] == "spice" {
				audiodev["driver"] = "sdl"
				// These settings might mitigate clipping?
				inOut := map[string]any{
					"frequency": 48000,
					"format": "f32",
				}
				audiodev["in"] = inOut
				audiodev["out"] = inOut
			}

			serialized, err := json.Marshal(audiodev)
			if err != nil {
				log.Fatalf("failed to marshal audiodev: %v", err)
			}

			newArgs = append(newArgs, string(serialized))
			args = args[1:]

			continue
		}

		if args[0] == "-chardev" {
			chardev := args[1]

			if strings.HasPrefix(chardev, "pty,") {
				// Expose serial console to GUI
				chardev = strings.TrimPrefix(chardev, "pty,")
				chardev = "vc," + chardev
			} else if strings.Contains(chardev, "-fs.sock") {
				// Handle virtiofs
				opts := strings.Split(chardev, ",")

				fsName := ""
				fsNum := -1
				for _, opt := range opts {
					if strings.HasPrefix(opt, "path=") {
						fsNameParts := strings.Split(opt, "/")
						fsName = fsNameParts[len(fsNameParts)-1]

						fsName = strings.TrimPrefix(fsName, "fs")
						fsName = strings.TrimSuffix(fsName, "-fs.sock")

						fsNum, err = strconv.Atoi(fsName)
						if err != nil {
							log.Fatalf("failed to parse filesystem ID: %v", err)
						}

						break
					}
				}

				paths := fsPaths(xmlDesc)
				path := paths[fsNum]

				socketPath := filepath.Join(virtiofsdDir, "fs" + fsName + "-fs.sock")
				virtiofsdSockets = append(virtiofsdSockets, socketPath)

				if *verbose {
					log.Printf("virtiofs: %s, cache %s, readonly %v", path.dir, path.caching, path.readOnly)
				}

				args := []string{"--socket-path", socketPath, "--shared-dir", path.dir, "--cache", path.caching}
				if path.readOnly {
					args = append(args, "--readonly")
				}

				virtiofsdCmds = append(virtiofsdCmds, exec.Command("/usr/lib/qemu/virtiofsd", args...))

				newOpts := []string{}
				for _, opt := range opts {
					if strings.HasPrefix(opt, "path=") {
						newOpts = append(newOpts, "path=" + socketPath)
						continue
					}

					newOpts = append(newOpts, opt)
				}

				chardev = strings.Join(newOpts, ",")
			} else if strings.Contains(chardev, "/monitor.sock") {
				// Remove monitor
				args = args[2:]
				continue
			} else if strings.Contains(chardev, "/org.qemu.guest_agent") {
				// Remove guest agent
				args = args[2:]
				continue
			}

			newArgs = append(newArgs, args[0])
			args = args[1:]

			newArgs = append(newArgs, chardev)
			args = args[1:]

			continue
		}

		// Remove serial port
		if args[0] == "-device" {
			device := map[string]any{}
			err = json.Unmarshal([]byte(args[1]), &device)
			if err != nil {
				log.Fatalf("failed to parse device: %v", err)
			}

			if device["driver"] == "virtserialport" {
				args = args[2:]
				continue
			}

			newArgs = append(newArgs, args[0])
			args = args[1:]

			newArgs = append(newArgs, args[0])
			args = args[1:]

			continue
		}

		// Remove monitor
		if args[0] == "-mon" {
			args = args[2:]
			continue
		}

		// Replace TAP network with bridge
		if args[0] == "-netdev" {
			newArgs = append(newArgs, args[0])
			args = args[1:]

			netdev := map[string]any{}
			err = json.Unmarshal([]byte(args[0]), &netdev)
			if err != nil {
				log.Fatalf("failed to parse netdev: %v", err)
			}

			if netdev["type"] == "tap" {
				netNum, err := strconv.Atoi(strings.TrimPrefix(netdev["id"].(string), "hostnet"))
				if err != nil {
					log.Fatalf("failed to parse hostnet ID: %v", err)
				}

				names := common.InterfaceNames(xmlDesc)

				net, err := l.NetworkLookupByName(names[netNum])
				if err != nil {
					log.Fatalf("failed to lookup network: %v", err)
				}

				bridge, err := l.NetworkGetBridgeName(net)

				netdev["type"] = "bridge"

				netdev["br"] = bridge

				delete(netdev, "fd")
				delete(netdev, "vhost")
				delete(netdev, "vhostfd")
			}

			serialized, err := json.Marshal(netdev)
			if err != nil {
				log.Fatalf("failed to marshal netdev: %v", err)
			}

			newArgs = append(newArgs, string(serialized))
			args = args[1:]

			continue
		}

		// Exit on guest shutdown
		if args[0] == "-no-shutdown" {
			args = args[1:]
			continue
		}

		// Remove AES key
		if args[0] == "-object" {
			object := map[string]any{}
			err = json.Unmarshal([]byte(args[1]), &object)
			if err != nil {
				log.Fatalf("failed to parse object: %v", err)
			}

			if object["qom-type"] == "secret" {
				args = args[2:]
				continue
			}

			newArgs = append(newArgs, args[0])
			args = args[1:]

			newArgs = append(newArgs, args[0])
			args = args[1:]

			continue
		}

		// Don't pause initially
		if args[0] == "-S" {
			args = args[1:]
			continue
		}

		// Remove sandbox
		// TODO: is there a way to restore this?
		if args[0] == "-sandbox" {
			args = args[2:]
			continue
		}

		// Remove SPICE
		if args[0] == "-spice" {
			args = args[2:]
			continue
		}

		newArgs = append(newArgs, args[0])
		args = args[1:]
	}

	if *verbose {
		for _, arg := range newArgs {
			log.Println(arg)
		}
	}

	qemuUid := -1
	if !*runAsRoot {
		qemuUser, err := user.Lookup(*qemuUsername)
		if err != nil {
			log.Fatalf("failed to lookup qemu user: %v", err)
		}

		qemuUid, err = strconv.Atoi(qemuUser.Uid)
		if err != nil {
			log.Fatalf("failed to parse qemu UID: %v", err)
		}
	}

	// Run virtiofsd
	for _, cmd := range virtiofsdCmds {
		go runHelperCmd(cmd)
	}

	// Wait for virtiofsd socket creation
	for _, sock := range virtiofsdSockets {
		for {
			if _, err := os.Stat(sock); errors.Is(err, os.ErrNotExist) {
				time.Sleep(time.Millisecond)
				continue
			} else {
				if !*runAsRoot {
					// TODO: Try using --socket-group option in virtiofsd command instead of manually doing chown?
					err = os.Chown(virtiofsdDir, qemuUid, -1)
					if err != nil {
						log.Fatalf("failed to chown virtiofsd socket dir: %v", err)
					}

					err = os.Chown(sock, qemuUid, -1)
					if err != nil {
						log.Fatalf("failed to chown virtiofsd socket: %v", err)
					}
				}

				break
			}
		}
	}

	var waypipeDir string

	// Launch Waypipe as GUI user
	if !*runAsRoot && *displayServer == "wayland" {
		if *verbose {
			log.Println("Dropping privileges on Wayland...")
		}

		waypipeDir, err = os.MkdirTemp("", "virt-gtk-waypipe")
		if err != nil {
			log.Fatalf("failed to create waypipe socket directory: %v", err)
		}
		defer os.RemoveAll(waypipeDir) // clean up

		err = os.MkdirAll(waypipeDir + "/xdg", 0o700)
		if err != nil {
			log.Fatalf("failed to create waypipe xdg directory: %v", err)
		}

		// SUDO_USER is not available from pkexec, but SUDO_UID is
		guiUidStr := os.Getenv("SUDO_UID")
		if guiUidStr == "" {
			log.Fatalf("failed to get GUI UID")
		}

		guiUser, err := user.LookupId(guiUidStr)
		if err != nil {
			log.Fatalf("failed to lookup GUI user: %v", err)
		}

		guiUid, err := strconv.Atoi(guiUidStr)
		if err != nil {
			log.Fatalf("failed to parse GUI UID: %v", err)
		}

		err = os.Chown(waypipeDir, guiUid, -1)
		if err != nil {
			log.Fatalf("failed to chown Waypipe socket directory: %v", err)
		}

		cmdWaypipe := exec.Command("sudo", "-u", guiUser.Username, "-E", "waypipe", "-s", waypipeDir + "/sock", "client")

		go runHelperCmd(cmdWaypipe)
		defer killHelperCmd(cmdWaypipe)

		// Wait for Waypipe socket creation
		for {
			if _, err := os.Stat(waypipeDir + "/sock"); errors.Is(err, os.ErrNotExist) {
				time.Sleep(time.Millisecond)
				continue
			} else {
				break
			}
		}

		err = os.Chown(waypipeDir, qemuUid, -1)
		if err != nil {
			log.Fatalf("failed to chown Waypipe socket directory: %v", err)
		}

		err = os.Chown(waypipeDir + "/xdg", qemuUid, -1)
		if err != nil {
			log.Fatalf("failed to chown Waypipe xdg directory: %v", err)
		}

		err = os.Chown(waypipeDir + "/sock", qemuUid, -1)
		if err != nil {
			log.Fatalf("failed to chown Waypipe socket: %v", err)
		}
	}

	// TODO: Try sandboxing QEMU (maybe pledge or bubblewrap?) so that it doesn't have full privs of qemuUsername.
	// Actually based on "ps -aux | grep waypipe" it looks like QEMU may already be using bubblewrap internally...? Need to look into this.

	cmdQemu := exec.Command(qemuExe, newArgs...)

	// Launch Qemu via Waypipe
	if !*runAsRoot && *displayServer == "wayland" {
		// TODO: See if we can prevent users besides QEMU and main user from accessing PulseAudio socket
		waypipeArgs := []string{"-u", *qemuUsername, "XDG_RUNTIME_DIR=" + waypipeDir + "/xdg", "PULSE_SERVER=unix:/tmp/pulse-socket", "waypipe", "-s", waypipeDir + "/sock", "server", "--", qemuExe}
		waypipeArgs = append(waypipeArgs, newArgs...)

		cmdQemu = exec.Command("sudo", waypipeArgs...)
	}

	if *displayServer == "x11" {
		cmdQemu.Env = append(os.Environ(),
			"GDK_BACKEND=x11",
		)
	}

	var qemuOut strings.Builder
	cmdQemu.Stdout = &qemuOut
	cmdQemu.Stderr = &qemuOut
	err = cmdQemu.Run()
	if *verbose {
		log.Printf("Qemu returned %v, %s", err, qemuOut.String())
	}
	if err != nil {
		log.Fatalf("failed to run Qemu: %v, %s", err, qemuOut.String())
	}
}
