/*
// Copyright (c) 2016 Intel Corporation
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

/* TODO

5. Install kernel
12. Make most output from osprepare optional
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/intel/ccloudvm/ccvm"
)

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "%s [create|start|stop|quit|status|connect|delete]\n\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "- create : creates a new VM")
		fmt.Fprintln(os.Stderr, "- start : boots a stopped VM")
		fmt.Fprintln(os.Stderr, "- stop : cleanly powers down a running VM")
		fmt.Fprintln(os.Stderr, "- quit : quits a running VM")
		fmt.Fprintln(os.Stderr, "- status : prints status information about the ccloudvm VM")
		fmt.Fprintln(os.Stderr, "- connect : connects to the VM via SSH")
		fmt.Fprintln(os.Stderr, "- delete : shuts down and deletes the VM")
	}
}

func runCommand(signalCh <-chan os.Signal) error {
	var err error

	errCh := make(chan error)
	ctx, cancelFunc := context.WithCancel(context.Background())
	switch os.Args[1] {
	case "create":
		go ccvm.Create(ctx, errCh)
	case "start":
		go ccvm.Start(ctx, errCh)
	case "stop":
		go ccvm.Stop(ctx, errCh)
	case "quit":
		go ccvm.Quit(ctx, errCh)
	case "status":
		go ccvm.Status(ctx, errCh)
	case "connect":
		go ccvm.Connect(ctx, errCh)
	case "delete":
		go ccvm.Delete(ctx, errCh)
	}
	select {
	case <-signalCh:
		cancelFunc()
		err = <-errCh
	case err = <-errCh:
		cancelFunc()
	}

	return err
}

func main() {
	flag.Parse()
	if len(os.Args) < 2 ||
		!(os.Args[1] == "create" || os.Args[1] == "start" || os.Args[1] == "stop" ||
			os.Args[1] == "quit" || os.Args[1] == "status" ||
			os.Args[1] == "connect" || os.Args[1] == "delete") {
		flag.Usage()
		os.Exit(1)
	}

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	if err := runCommand(signalCh); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
