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
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/digitalocean/go-libvirt"
	"github.com/mattn/go-shellwords"

	"github.com/JeremyRand/virt-gtk/common"
)

// TODO: don't run as root
// TODO: handle X11 and/or Wayland
// TODO: find a way to make KeePassXC auto-type work with Wayland

func fsPaths(xmlDesc string) []string {
	type Driver struct {
		DType string `xml:"type,attr"`
	}

	type Source struct {
		Dir string `xml:"dir,attr"`
	}

	type Filesystem struct {
		Driver Driver `xml:"driver"`
		Source Source `xml:"source"`
	}

	type Result struct {
		Filesystems []Filesystem `xml:"devices>filesystem"`
	}

	result := Result{}

	err := xml.Unmarshal([]byte(xmlDesc), &result)

	if err != nil {
		log.Fatalf("failed to parse filesystems from XML: %v", err)
	}

	paths := []string{}

	for _, fs := range result.Filesystems {
		if fs.Driver.DType == "virtiofs" {
			paths = append(paths, fs.Source.Dir)
		}
	}

	return paths
}

func runHelperCmd(cmd *exec.Cmd) {
	var out strings.Builder
	cmd.Stderr = &out

	err := cmd.Run()
	if err != nil {
		log.Fatalf("failed to run virtiofsd: %v, %s", err, out.String())
	}
}

func main() {
	uri, _ := url.Parse(string(libvirt.QEMUSystem))
	l, err := libvirt.ConnectToURI(uri)
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}

	defer l.Disconnect()

	domain, err := l.DomainLookupByName(os.Args[1])
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
		// TODO: Should we use PulseAudio or something instead? SDL seems to be unrecommended by QEMU devs.
		if args[0] == "-audiodev" {
			newArgs = append(newArgs, args[0])
			args = args[1:]

			audiodev := map[string]any{}
			err = json.Unmarshal([]byte(args[0]), &audiodev)
			if err != nil {
				log.Fatalf("failed to parse audiodev: %v", err)
			}

			if audiodev["driver"] == "spice" {
				// TODO: fix this for use without sudo -E
				// TODO: fix SDL audio for non-root operation by libvirt-qemu user
				//audiodev["driver"] = "sdl"
				audiodev["driver"] = "none"
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
				virtiofsdCmds = append(virtiofsdCmds, exec.Command("/usr/lib/qemu/virtiofsd", "--socket-path=" + socketPath, "-o", "source=" + path, "-o", "cache=always"))

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

	for _, arg := range newArgs {
		log.Println(arg)
	}

	// Run virtiofsd
	for _, cmd := range virtiofsdCmds {
		go runHelperCmd(cmd)
	}

	for _, sock := range virtiofsdSockets {
		for {
			if _, err := os.Stat(sock); errors.Is(err, os.ErrNotExist) {
				time.Sleep(time.Millisecond)
				continue
			} else {
				// TODO: run chown on the socket
				break
			}
		}
	}

	// TODO: Try sandboxing QEMU (maybe pledge or bubblewrap?) so that it doesn't have full privs of QEMU_USER.

	cmdQemu := exec.Command(qemuExe, newArgs...)
	var qemuOut strings.Builder
	cmdQemu.Stderr = &qemuOut
	err = cmdQemu.Run()
	if err != nil {
		log.Fatalf("failed to run Qemu: %v, %s", err, qemuOut.String())
	}
}
