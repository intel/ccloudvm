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

	"github.com/intel/ccloudvm/ccvm"
	"github.com/intel/ccloudvm/types"
	"github.com/spf13/cobra"
)

var startSpec types.VMSpec
var startMOptsSpec multiOptions

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Boots a stopped VM",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancelFunc := getSignalContext()
		defer cancelFunc()

		mergeVMOptions(&startSpec, &startMOptsSpec)
		return ccvm.Start(ctx, &startSpec)
	},
}

func init() {
	rootCmd.AddCommand(startCmd)

	var flags flag.FlagSet
	vmFlags(&flags, &startSpec, &startMOptsSpec)

	startCmd.Flags().AddGoFlagSet(&flags)
}
