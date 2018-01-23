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

package cmd

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/intel/ccloudvm/types"
	"github.com/pkg/errors"
)

type mounts []types.Mount
type ports []types.PortMapping
type drives []types.Drive

type multiOptions struct {
	m mounts
	p ports
	d drives
}

func (m *mounts) String() string {
	return fmt.Sprint(*m)
}

func (m *mounts) Set(value string) error {
	components := strings.Split(value, ",")
	if len(components) != 3 {
		return fmt.Errorf("--mount parameter should be of format tag,security_model,path")
	}
	*m = append(*m, types.Mount{
		Tag:           components[0],
		SecurityModel: components[1],
		Path:          components[2],
	})
	return nil
}

func (p *ports) String() string {
	return fmt.Sprint(*p)
}

func (p *ports) Set(value string) error {
	components := strings.Split(value, "-")
	if len(components) != 2 {
		return fmt.Errorf("--port parameter should be of format host-guest")
	}
	host, err := strconv.Atoi(components[0])
	if err != nil {
		return fmt.Errorf("host port must be a number")
	}
	guest, err := strconv.Atoi(components[1])
	if err != nil {
		return fmt.Errorf("guest port must be a number")
	}
	*p = append(*p, types.PortMapping{
		Host:  host,
		Guest: guest,
	})
	return nil
}

func (d *drives) String() string {
	return fmt.Sprint(*d)
}

func (d *drives) Set(value string) error {
	components := strings.Split(value, ",")
	if len(components) < 2 {
		return fmt.Errorf("--drive parameter should be of format path,format[,option]*")
	}
	_, err := os.Stat(components[0])
	if err != nil {
		return errors.Wrapf(err, "Unable to access %s", components[1])
	}
	*d = append(*d, types.Drive{
		Path:    components[0],
		Format:  components[1],
		Options: strings.Join(components[2:], ","),
	})
	return nil
}

func mergeVMOptions(vmSpec *types.VMSpec, mOpts *multiOptions) {
	vmSpec.PortMappings = []types.PortMapping(mOpts.p)
	vmSpec.Drives = []types.Drive(mOpts.d)
	vmSpec.Mounts = []types.Mount(mOpts.m)
}

func vmFlags(fs *flag.FlagSet, customSpec *types.VMSpec, mOpts *multiOptions) {
	fs.IntVar(&customSpec.MemMiB, "mem", customSpec.MemMiB, "Mebibytes of RAM allocated to VM")
	fs.IntVar(&customSpec.CPUs, "cpus", customSpec.CPUs, "VCPUs assigned to VM")
	fs.Var(&mOpts.m, "mount", "directory to mount in guest VM via 9p. Format is tag,security_model,path")
	fs.Var(&mOpts.d, "drive", "Host accessible resource to appear as block device in guest VM.  Format is path,format[,option]*")
	fs.Var(&mOpts.p, "port", "port mapping. Format is host_port-guest_port, e.g., -port 10022-22")
	fs.UintVar(&customSpec.Qemuport, "qemuport", customSpec.Qemuport, "Port to follow qemu logs of the guest machine, eg., --qemuport=9999")
}
