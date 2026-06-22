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

package common

import (
	"encoding/xml"
	"log"
)

func InterfaceNames(xmlDesc string) []string {
	type Source struct {
		Network string `xml:"network,attr"`
	}

	type Iface struct {
		IType string `xml:"type,attr"`
		Source Source `xml:"source"`
	}

	type Result struct {
		Ifaces []Iface `xml:"devices>interface"`
	}

	result := Result{}

	err := xml.Unmarshal([]byte(xmlDesc), &result)

	if err != nil {
		log.Fatalf("failed to parse interfaces from XML: %v", err)
	}

	names := []string{}

	for _, iface := range result.Ifaces {
		if iface.IType == "network" {
			names = append(names, iface.Source.Network)
		}
	}

	return names
}
