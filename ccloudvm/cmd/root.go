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
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ccloudvm",
	Short: "Configurable Cloud VM (ccloudvm) allows the creation and management of VMs from cloud-init files",
}

// Execute is the entry into the cmd package from the main package.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
}

func getSignalContext() (context.Context, context.CancelFunc) {
	ctx, cancelFunc := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 1)
	go func() {
		for {
			select {
			case <-sigCh:
				cancelFunc()
			case <-ctx.Done():
				break
			}
		}
	}()
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	return ctx, cancelFunc
}
