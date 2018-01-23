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

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Prints status information about a VM",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancelFunc := getSignalContext()
		defer cancelFunc()

		return client.Status(ctx)
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
