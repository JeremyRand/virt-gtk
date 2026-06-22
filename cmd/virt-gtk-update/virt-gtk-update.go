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

// Packagers should run:
// sudo adduser libvirt-qemu audio
// sudo adduser libvirt-qemu netdev

// You also need to set a capability on the QEMU bridge helper if network
// support is needed, like this:
// sudo setcap 'cap_net_admin+eip' /usr/lib/qemu/qemu-bridge-helper
// Do not enable the setuid attribute on that helper like the QEMU docs say
// here: https://wiki.qemu.org/Features/HelperNetworking#Setup
// setcap works fine and has less attack surface.

package main

import (
	"encoding/xml"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/digitalocean/go-libvirt"

	"github.com/JeremyRand/virt-gtk/common"
)

func imagePaths(xmlDesc string) []string {
	type Source struct {
		File string `xml:"file,attr"`
	}

	type Result struct {
		Sources []Source `xml:"devices>disk>source"`
	}

	result := Result{}

	err := xml.Unmarshal([]byte(xmlDesc), &result)

	if err != nil {
		log.Fatalf("failed to parse block images from XML: %v", err)
	}

	paths := []string{}

	for _, source := range result.Sources {
		paths = append(paths, source.File)
	}

	return paths
}

func main() {
	uri, _ := url.Parse(string(libvirt.QEMUSystem))
	l, err := libvirt.ConnectToURI(uri)
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}

	defer l.Disconnect()

	flags := libvirt.ConnectListAllDomainsFlags(0)
	domains, _, err := l.ConnectListAllDomains(1, flags)
	if err != nil {
		log.Fatalf("failed to retrieve domains: %v", err)
	}

	err = os.RemoveAll("/usr/local/share/applications/virt-gtk")
	if err != nil {
		log.Fatalf("failed to remove old launchers: %v", err)
	}

	// TODO: this needs root. Can we avoid needing root?
	err = os.MkdirAll("/usr/local/share/applications/virt-gtk", 0o755)
	if err != nil {
		log.Fatalf("failed to create launcher directory: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "virt-gtk")
	if err != nil {
		log.Fatalf("failed to create tmp launcher directory: %v", err)
	}
	defer os.RemoveAll(tmpDir) // clean up

	for _, d := range domains {
		fmt.Printf("%s\n", d.Name)

		xmlDesc, err := l.DomainGetXMLDesc(d, libvirt.DomainXMLSecure | libvirt.DomainXMLInactive)
		if err != nil {
			log.Fatalf("failed to get XML: %v", err)
		}

		// Set permissions for block device images
		for _, imagePath := range imagePaths(xmlDesc) {
			libvirtUser, err := user.Lookup("libvirt-qemu")
			if err != nil {
				log.Fatalf("failed to lookup libvirt user: %v", err)
			}

			libvirtUid, err := strconv.Atoi(libvirtUser.Uid)
			if err != nil {
				log.Fatalf("failed to parse libvirt UID: %v", err)
			}

			libvirtGroup, err := user.LookupGroup("libvirt-qemu")
			if err != nil {
				log.Fatalf("failed to lookup libvirt group: %v", err)
			}

			libvirtGid, err := strconv.Atoi(libvirtGroup.Gid)
			if err != nil {
				log.Fatalf("failed to parse libvirt GID: %v", err)
			}

			err = os.Chown(imagePath, libvirtUid, libvirtGid)
			if err != nil {
				log.Fatalf("failed to set permissions on block device image: %v", err)
			}
		}

		// Set permissions for network interfaces
		ifaceRange:
		for _, iface := range common.InterfaceNames(xmlDesc) {
			net, err := l.NetworkLookupByName(iface)
			if err != nil {
				log.Fatalf("failed to lookup network: %v", err)
			}

			bridge, err := l.NetworkGetBridgeName(net)

			err = os.MkdirAll("/etc/qemu", 0o755)
			if err != nil {
				log.Fatalf("failed to create qemu config directory: %v", err)
			}

			config, err := os.ReadFile("/etc/qemu/bridge.conf")
			if err == nil {
				for _, line := range strings.Split(string(config), "\n") {
					if line == "allow " + bridge {
						continue ifaceRange
					}
				}
			}

			fConfig, err := os.OpenFile("/etc/qemu/bridge.conf", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o755)
			if err != nil {
				log.Fatalf("failed to open qemu bridge config file: %v", err)
			}

			fmt.Fprintf(fConfig, "allow %s\n", bridge)

			err = fConfig.Close()
			if err != nil {
				log.Fatalf("failed to close qemu bridge config file: %v", err)
			}
		}

		filePath := filepath.Join(tmpDir, d.Name + ".desktop")

		f, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE, 0o755)
		if err != nil {
			log.Fatalf("failed to create launcher file: %v", err)
		}

		fmt.Fprintln(f, "[Desktop Entry]")
		fmt.Fprintln(f, "Comment=Virtual Machine")
		fmt.Fprintf(f, "Exec=sh -c \"pkexec env DISPLAY=\\$DISPLAY XAUTHORITY=\\$XAUTHORITY virt-gtk %s\"\n", d.Name) // TODO: clean this up once virt-gtk main executable is ported to Go
		//fmt.Fprintln(f, "TryExec=virt-gtk") // TODO: test and enable this
		fmt.Fprintln(f, "Icon=qemu")
		fmt.Fprintln(f, "MimeType=")
		fmt.Fprintf(f, "Name=%s\n", d.Name)
		fmt.Fprintln(f, "Categories=System;")
		fmt.Fprintln(f, "StartupNotify=true") // TODO: do we handle this properly?
		fmt.Fprintln(f, "Terminal=false")
		fmt.Fprintln(f, "Type=Application")
		// TODO: Actions, StartupWMClass, SingleMainWindow

		err = f.Close()
		if err != nil {
			log.Fatalf("failed to close launcher file: %v", err)
		}

		cmd := exec.Command("desktop-file-install", "--dir=/usr/local/share/applications/virt-gtk", filePath)
		err = cmd.Run()
		if err != nil {
			log.Fatalf("failed to install launcher file: %v", err)
		}
	}

	cmdUpdate := exec.Command("update-desktop-database")
	err = cmdUpdate.Run()
	if err != nil {
		log.Fatalf("failed to update launcher database: %v", err)
	}
}
