//
// Copyright (c) 2018 Intel Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package types

import (
	"fmt"
	"net"
	"os"
	"path"

	"github.com/pkg/errors"
)

// PortMapping exposes a guest resident service on the host
type PortMapping struct {
	Host  int `yaml:"host"`
	Guest int `yaml:"guest"`
}

func (p PortMapping) String() string {
	return fmt.Sprintf("%d-%d", p.Host, p.Guest)
}

// Mount contains information about a host path to be mounted inside the guest
type Mount struct {
	Tag           string `yaml:"tag"`
	SecurityModel string `yaml:"security_model"`
	Path          string `yaml:"path"`
}

func (m Mount) String() string {
	return fmt.Sprintf("%s,%s,%s", m.Tag, m.SecurityModel, m.Path)
}

// Drive contains information about additional drives to mount in the guest
type Drive struct {
	Path    string
	Format  string
	Options string
}

func (d Drive) String() string {
	return fmt.Sprintf("%s,%s,%s", d.Path, d.Format, d.Options)
}

// VMSpec holds the per-VM state.
type VMSpec struct {
	MemMiB       int           `yaml:"mem_mib"`
	DiskGiB      int           `yaml:"disk_gib"`
	CPUs         int           `yaml:"cpus"`
	PortMappings []PortMapping `yaml:"ports"`
	Mounts       []Mount       `yaml:"mounts"`
	Drives       []Drive       `yaml:"drives"`
	Qemuport     uint          `yaml:"qemuport"`
	HostIP       net.IP        `yaml:"host_ip"`
}

// CheckDirectory checks to see if a given absolute path exists and is
// a directory.
func CheckDirectory(dir string) error {
	if dir == "" {
		return nil
	}

	if !path.IsAbs(dir) {
		return fmt.Errorf("%s is not an absolute path", dir)
	}

	fi, err := os.Stat(dir)
	if err != nil {
		return errors.Wrapf(err, "Unable to stat %s", dir)
	}

	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}

	return nil
}

// MergeMounts merges a slice of mounts into an existing VMSpec.  Mounts
// supplied in the m parameter override existing mounts in the VMSpec.
func (in *VMSpec) MergeMounts(m []Mount) {
	mountCount := len(in.Mounts)
	for _, mount := range m {
		var i int
		for i = 0; i < mountCount; i++ {
			if mount.Tag == in.Mounts[i].Tag {
				break
			}
		}

		if i == mountCount {
			in.Mounts = append(in.Mounts, mount)
		} else {
			in.Mounts[i] = mount
		}
	}
}

// MergePorts merges a slice of ports into an existing VMSpec.  Ports
// supplied in the p parameter override existing ports in the VMSpec.
func (in *VMSpec) MergePorts(p []PortMapping) {
	portCount := len(in.PortMappings)
	for _, port := range p {
		var i int
		for i = 0; i < portCount; i++ {
			if port.Guest == in.PortMappings[i].Guest {
				break
			}
		}

		if i == portCount {
			in.PortMappings = append(in.PortMappings, port)
		} else {
			in.PortMappings[i] = port
		}
	}
}

// MergeDrives merges a slice of drives into an existing VMSpec.  Drives
// supplied in the d parameter override existing drives in the VMSpec.
func (in *VMSpec) MergeDrives(d []Drive) {
	driveCount := len(in.Drives)
	for _, drive := range d {
		var i int
		for i = 0; i < driveCount; i++ {
			if drive.Path == in.Drives[i].Path {
				break
			}
		}

		if i == driveCount {
			in.Drives = append(in.Drives, drive)
		} else {
			in.Drives[i] = drive
		}
	}
}

// MergeCustom merges one VMSpec into another.  In addition to merging
// mounts, drives and ports, other fields in the receiver VM spec, such as
// MemMiB, are also updated, with values provided by the customSpec parameter,
// if they are not already defined.
func (in *VMSpec) MergeCustom(customSpec *VMSpec) error {
	for i := range customSpec.Mounts {
		if err := CheckDirectory(customSpec.Mounts[i].Path); err != nil {
			return err
		}
	}

	if customSpec.MemMiB != 0 {
		in.MemMiB = customSpec.MemMiB
	}
	if customSpec.CPUs != 0 {
		in.CPUs = customSpec.CPUs
	}
	if customSpec.DiskGiB != 0 {
		in.DiskGiB = customSpec.DiskGiB
	}
	if customSpec.Qemuport != 0 {
		in.Qemuport = customSpec.Qemuport
	}

	/* We don't allow host ips to be specified in the workload definition */

	if len(customSpec.HostIP) == 0 {
		return errors.New("HostIP not defined")
	}
	in.HostIP = customSpec.HostIP

	in.MergeMounts(customSpec.Mounts)
	in.MergePorts(customSpec.PortMappings)
	in.MergeDrives(customSpec.Drives)

	return nil
}

// SSHPort returns the port on the host which can be used to access the instance
// described by the VMSpec.
func (in *VMSpec) SSHPort() (int, error) {
	for _, p := range in.PortMappings {
		if p.Guest == 22 {
			return p.Host, nil
		}
	}
	return 0, fmt.Errorf("No SSH port configured")
}

// Merge from a parent spec into the current spec
func (in *VMSpec) Merge(parent *VMSpec) {
	if in.MemMiB == 0 {
		in.MemMiB = parent.MemMiB
	}

	if in.CPUs == 0 {
		in.CPUs = parent.CPUs
	}
	if in.DiskGiB == 0 {
		in.DiskGiB = parent.DiskGiB
	}
	if in.Qemuport == 0 {
		in.Qemuport = parent.Qemuport
	}

	in.MergeMounts(parent.Mounts)
	in.MergePorts(parent.PortMappings)
	in.MergeDrives(parent.Drives)
}
