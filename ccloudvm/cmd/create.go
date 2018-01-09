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

	"github.com/intel/ccloudvm"
	"github.com/spf13/cobra"
)

var createSpec ccloudvm.VMSpec
var createDebug bool
var createPackageUpgrade bool

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Creates a new VM",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancelFunc := getSignalContext()
		defer cancelFunc()

		return ccloudvm.Create(ctx, args[0], &createSpec, createDebug, createPackageUpgrade)
	},
}

func init() {
	rootCmd.AddCommand(createCmd)

	var flags flag.FlagSet
	ccloudvm.VMFlags(&flags, &createSpec)

	createCmd.Flags().AddGoFlagSet(&flags)
	createCmd.Flags().Bool("debug", false, "Enable debugging mode")
	createCmd.Flags().Bool("package-upgrade", true, "Hint as to whether to upgrade packages on creation")
}
