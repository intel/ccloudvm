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
	"net/url"
	"strings"
	"text/template"

	"github.com/intel/ccloudvm/types"
	"github.com/pkg/errors"
	yaml "gopkg.in/yaml.v2"
)

const defaultRootFSSize = 60

// Constants for the Guest image used by ccloudvm

const (
	guestDownloadURL       = "https://cloud-images.ubuntu.com/xenial/current/xenial-server-cloudimg-amd64-disk1.img"
	guestImageFriendlyName = "Ubuntu 16.04"
	defaultHostname        = "singlevm"
)

type workloadSpec struct {
	BaseImageURL  string       `yaml:"base_image_url"`
	BaseImageName string       `yaml:"base_image_name"`
	Hostname      string       `yaml:"hostname"`
	WorkloadName  string       `yaml:"workload"`
	NeedsNestedVM bool         `yaml:"needs_nested_vm"`
	VM            types.VMSpec `yaml:"vm"`
}

func unmarshal(in *types.VMSpec, data []byte) error {
	err := yaml.Unmarshal(data, in)
	if err != nil {
		return errors.Wrap(err, "Unable to unmarshal instance state")
	}

	for i := range in.Mounts {
		if err := types.CheckDirectory(in.Mounts[i].Path); err != nil {
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
			types.PortMapping{
				Host:  10022,
				Guest: 22,
			})
	}

	return nil
}

func unmarshalWithTemplate(in *types.VMSpec, ws *workspace, data string) error {
	tmpl, err := template.New("instance-data").Parse(string(data))
	if err != nil {
		return errors.Wrap(err, "Unable to parse instance data template")
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, ws)
	if err != nil {
		return errors.Wrap(err, "Unable to execute instance data template")
	}
	return unmarshal(in, buf.Bytes())
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
