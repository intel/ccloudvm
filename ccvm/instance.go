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

package main

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
)

type workloadSpec struct {
	BaseImageURL  string       `yaml:"base_image_url"`
	BaseImageName string       `yaml:"base_image_name"`
	WorkloadName  string       `yaml:"workload"`
	NeedsNestedVM bool         `yaml:"needs_nested_vm"`
	BIOS          string       `yaml:"bios"`
	VM            types.VMSpec `yaml:"vm"`
	Inherits      string       `yaml:"inherits"`
}

func defaultVMSpec() types.VMSpec {
	memDef, cpuDef := 1024, 1

	return types.VMSpec{
		MemMiB:  memDef,
		CPUs:    cpuDef,
		DiskGiB: defaultRootFSSize,
	}
}

func defaultWorkload() *workload {
	return &workload{
		spec: workloadSpec{
			BaseImageName: guestImageFriendlyName,
			BaseImageURL:  guestDownloadURL,
			VM:            defaultVMSpec(),
		},
	}
}

func (ins *workloadSpec) unmarshal(data []byte) error {
	err := yaml.Unmarshal(data, ins)
	if err != nil {
		return errors.Wrap(err, "Unable to unmarshal instance specification")
	}

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
