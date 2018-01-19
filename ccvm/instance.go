//
// Copyright (c) 2017 Intel Corporation
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

package ccvm

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/pkg/errors"
	yaml "gopkg.in/yaml.v2"
)

// Different types of virtual development environments
// we support.
const (
	CIAO            = "ciao"
	CLEARCONTAINERS = "clearcontainers"
)

const defaultRootFSSize = 60

// Constants for the Guest image used by ccloudvm

const (
	guestDownloadURL       = "https://cloud-images.ubuntu.com/xenial/current/xenial-server-cloudimg-amd64-disk1.img"
	guestImageFriendlyName = "Ubuntu 16.04"
	defaultHostname        = "singlevm"
)

type portMapping struct {
	Host  int `yaml:"host"`
	Guest int `yaml:"guest"`
}

func (p portMapping) String() string {
	return fmt.Sprintf("%d-%d", p.Host, p.Guest)
}

type mount struct {
	Tag           string `yaml:"tag"`
	SecurityModel string `yaml:"security_model"`
	Path          string `yaml:"path"`
}

func (m mount) String() string {
	return fmt.Sprintf("%s,%s,%s", m.Tag, m.SecurityModel, m.Path)
}

type drive struct {
	Path    string
	Format  string
	Options string
}

func (d drive) String() string {
	return fmt.Sprintf("%s,%s,%s", d.Path, d.Format, d.Options)
}

// VMSpec holds the per-VM state.
type VMSpec struct {
	MemMiB       int    `yaml:"mem_mib"`
	DiskGiB      int    `yaml:"disk_gib"`
	CPUs         int    `yaml:"cpus"`
	PortMappings ports  `yaml:"ports"`
	Mounts       mounts `yaml:"mounts"`
	Drives       drives `yaml:"drives"`
	Qemuport     uint   `yaml:"qemuport"`
}

type workloadSpec struct {
	BaseImageURL  string `yaml:"base_image_url"`
	BaseImageName string `yaml:"base_image_name"`
	Hostname      string `yaml:"hostname"`
	WorkloadName  string `yaml:"workload"`
	NeedsNestedVM bool   `yaml:"needs_nested_vm"`
	VM            VMSpec `yaml:"vm"`
}

// This function creates a default instanceData object for legacy ciao-down
// ciao VMs.  These old VMs did not store information about mounts and
// mapped ports as this information was hard-coded into ccloudvm itself.
// Consequently, when migrating one of these old VMs we need to fill in
// the missing information.
func (in *VMSpec) loadLegacyInstance(ws *workspace) error {
	// Check for legacy state files.

	data, err := ioutil.ReadFile(path.Join(ws.instanceDir, "vmtype.txt"))
	if err == nil {
		vmType := string(data)
		if vmType != CIAO && vmType != CLEARCONTAINERS {
			err := fmt.Errorf("Unsupported vmType %s. Should be one of "+CIAO+"|"+CLEARCONTAINERS, vmType)
			return err
		}
	}

	uiPath := ""
	data, err = ioutil.ReadFile(path.Join(ws.instanceDir, "ui_path.txt"))
	if err == nil {
		uiPath = string(data)
	}

	in.Mounts = []mount{
		{
			Tag:           "hostgo",
			SecurityModel: "passthrough",
			Path:          ws.GoPath,
		},
	}

	in.PortMappings = []portMapping{
		{
			Host:  10022,
			Guest: 22,
		},
	}

	if uiPath != "" {
		in.Mounts = append(in.Mounts, mount{
			Tag:           "hostui",
			SecurityModel: "mapped",
			Path:          filepath.Clean(uiPath),
		})
	}

	return nil
}

func (in *VMSpec) unmarshal(data []byte) error {
	err := yaml.Unmarshal(data, in)
	if err != nil {
		return errors.Wrap(err, "Unable to unmarshal instance state")
	}

	for i := range in.Mounts {
		if err := checkDirectory(in.Mounts[i].Path); err != nil {
			return fmt.Errorf("Bad mount %s specified: %v",
				in.Mounts[i].Path, err)
		}
	}

	var memDef, cpuDef int
	if in.MemMiB == 0 || in.CPUs == 0 {
		memDef, cpuDef = getMemAndCpus()
		if in.MemMiB == 0 {
			in.MemMiB = memDef
		}
		if in.CPUs == 0 {
			in.CPUs = cpuDef
		}
	}

	if in.DiskGiB == 0 {
		in.DiskGiB = defaultRootFSSize
	}

	var i int
	for i = 0; i < len(in.PortMappings); i++ {
		if in.PortMappings[i].Guest == 22 {
			break
		}
	}
	if i == len(in.PortMappings) {
		in.PortMappings = append(in.PortMappings,
			portMapping{
				Host:  10022,
				Guest: 22,
			})
	}

	return nil
}

func (in *VMSpec) unmarshalWithTemplate(ws *workspace, data string) error {
	tmpl, err := template.New("instance-data").Parse(string(data))
	if err != nil {
		return errors.Wrap(err, "Unable to parse instance data template")
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, ws)
	if err != nil {
		return errors.Wrap(err, "Unable to execute instance data template")
	}
	return in.unmarshal(buf.Bytes())
}

func (in *VMSpec) mergeMounts(m mounts) {
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

func (in *VMSpec) mergePorts(p ports) {
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

func (in *VMSpec) mergeDrives(d drives) {
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

func (in *VMSpec) mergeCustom(customSpec *VMSpec) error {
	for i := range customSpec.Mounts {
		if err := checkDirectory(customSpec.Mounts[i].Path); err != nil {
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

	in.mergeMounts(customSpec.Mounts)
	in.mergePorts(customSpec.PortMappings)
	in.mergeDrives(customSpec.Drives)

	return nil
}

func (in *VMSpec) sshPort() (int, error) {
	for _, p := range in.PortMappings {
		if p.Guest == 22 {
			return p.Host, nil
		}
	}
	return 0, fmt.Errorf("No SSH port configured")
}

func (ins *workloadSpec) unmarshal(data []byte) error {
	err := yaml.Unmarshal(data, ins)
	if err != nil {
		return errors.Wrap(err, "Unable to unmarshal instance specification")
	}

	if ins.BaseImageURL == "" {
		ins.BaseImageURL = guestDownloadURL
		ins.BaseImageName = guestImageFriendlyName
	} else {
		url, err := url.Parse(ins.BaseImageURL)
		if err != nil {
			return fmt.Errorf("Unable to parse url %s : %v",
				ins.BaseImageURL, err)
		}
		if ins.BaseImageName == "" {
			lastSlash := strings.LastIndex(url.Path, "/")
			if lastSlash == -1 {
				ins.BaseImageName = url.Path
			} else {
				ins.BaseImageName = url.Path[lastSlash+1:]
			}
		}
	}

	if ins.Hostname == "" {
		ins.Hostname = defaultHostname
	}
	return nil
}

func (ins *workloadSpec) unmarshalWithTemplate(ws *workspace, data string) error {
	tmpl, err := template.New("instance-spec").Parse(string(data))
	if err != nil {
		return errors.Wrap(err, "Unable to parse instance data template")
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, ws)
	if err != nil {
		return errors.Wrap(err, "Unable to execute instance data template")
	}
	return ins.unmarshal(buf.Bytes())
}
