/*
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
*/

package cmd

import (
	"flag"
	"net"

	"github.com/intel/ccloudvm/client"
	"github.com/intel/ccloudvm/types"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type ipAddr net.IP

func (ip *ipAddr) String() string {
	return (*net.IP)(ip).String()
}

func (ip *ipAddr) Type() string {
	return "string"
}

func (ip *ipAddr) Set(value string) error {
	addr := net.ParseIP(value)
	if len(addr) == 0 {
		return errors.Errorf("Invalid IP address [%s] specified", value)
	}
	addr = addr.To4()
	if len(addr) == 0 {
		return errors.Errorf("[%s] is not an IP4V address", value)
	}
	*ip = ipAddr(addr)
	return nil
}

var instanceName string
var createSpec types.VMSpec
var createMOptsSpec multiOptions
var createDebug bool
var createPackageUpgrade bool
var createHostIP ipAddr

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Creates a new VM",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancelFunc := getSignalContext()
		defer cancelFunc()

		mergeVMOptions(&createSpec, &createMOptsSpec)
		createSpec.HostIP = net.IP(createHostIP)
		return client.Create(ctx, instanceName, args[0], createDebug, createPackageUpgrade, &createSpec)
	},
}

func init() {
	rootCmd.AddCommand(createCmd)

	var flags flag.FlagSet
	vmFlags(&flags, &createSpec, &createMOptsSpec)
	flags.IntVar(&createSpec.DiskGiB, "disk", createSpec.DiskGiB, "Gibibytes of disk space allocated to Rootfs")

	createCmd.Flags().AddGoFlagSet(&flags)
	createCmd.Flags().StringVar(&instanceName, "name", "", "Name of new instance")
	createCmd.Flags().BoolVar(&createDebug, "debug", false, "Enable debugging mode")
	createCmd.Flags().BoolVar(&createPackageUpgrade, "package-upgrade", false, "Hint as to whether to upgrade packages on creation")
	createCmd.Flags().Var(&createHostIP, "hostip", "Host IP address on which instance services will be exposed")
}
