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
	"github.com/intel/ccloudvm/client"
	"github.com/spf13/cobra"
)

var recurse bool
var host bool

var copyCmd = &cobra.Command{
	Use:   "copy",
	Short: "Copy files between the host and the guest using scp",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancelFunc := getSignalContext()
		defer cancelFunc()

		var instanceName string
		if len(args) >= 3 {
			instanceName = args[0]
			args = args[1:]
		}

		return client.Copy(ctx, instanceName, recurse, host, args[0], args[1])
	},
}

func init() {
	rootCmd.AddCommand(copyCmd)

	copyCmd.Flags().BoolVarP(&recurse, "recurse", "r", false, "Recursively copy contents of a directory")
	copyCmd.Flags().BoolVarP(&host, "to-host", "t", false, "Copy files from guest to host")
}
