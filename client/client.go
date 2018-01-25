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

package client

import (
	"bufio"
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/rpc"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ciao-project/ciao/osprepare"
	"github.com/intel/ccloudvm/types"
	"github.com/pkg/errors"
)

type logger struct{}

func (l logger) V(int32) bool {
	return false
}

func (l logger) Infof(s string, args ...interface{}) {
	out := fmt.Sprintf(s, args...)
	fmt.Print(out)
	if !strings.HasSuffix(out, "\n") {
		fmt.Println()
	}
}

func (l logger) Warningf(s string, args ...interface{}) {
	l.Infof(s, args)
}

func (l logger) Errorf(s string, args ...interface{}) {
	l.Infof(s, args)
}

const systemdService = `
[Unit]
Description=Configurable CloudVM Service

[Service]
Type=simple
ExecStart=%s/bin/ccvm
KillMode=process
`

const systemdSocket = `
[Socket]
ListenStream=%s/.ccloudvm/socket

[Install]
WantedBy=sockets.target
`

// Setup Installs dependencies
func Setup(ctx context.Context) error {
	home := os.Getenv("HOME")
	if home == "" {
		return errors.New("HOME is not defined")
	}
	home = strings.TrimSpace(home)

	goPathBytes, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		return errors.Wrap(err, "Unable to determine GOPATH")
	}
	goPath := strings.TrimSpace(string(goPathBytes))

	fmt.Println("Installing host dependencies")
	osprepare.InstallDeps(ctx, ccloudvmDeps, logger{})

	systemdRootPath := filepath.Join(home, ".local/share/systemd/user")
	err = os.MkdirAll(systemdRootPath, 0700)
	if err != nil {
		return errors.Wrap(err, "Unable to create systemd directory")
	}

	servicePath := filepath.Join(systemdRootPath, "ccloudvm.service")
	serviceData := fmt.Sprintf(systemdService, goPath)
	err = ioutil.WriteFile(servicePath, []byte(serviceData), 0600)
	if err != nil {
		return errors.Wrap(err, "Unable to write service file")
	}

	socketPath := filepath.Join(systemdRootPath, "ccloudvm.socket")
	socketData := fmt.Sprintf(systemdSocket, home)
	err = ioutil.WriteFile(socketPath, []byte(socketData), 0600)
	if err != nil {
		return errors.Wrap(err, "Unable to write service file")
	}

	err = exec.Command("systemctl", "--user", "daemon-reload").Run()
	if err != nil {
		return errors.Wrap(err, "Unable to reload service files")
	}

	err = exec.Command("systemctl", "--user", "enable", "ccloudvm.socket").Run()
	if err != nil {
		return errors.Wrap(err, "Unable to enable ccloudvm.socket")
	}
	err = exec.Command("systemctl", "--user", "start", "ccloudvm.socket").Run()
	if err != nil {
		return errors.Wrap(err, "Unable to start ccloudvm.socket")
	}

	return nil
}

func issueCommand(ctx context.Context, call func(*rpc.Client) (int, error),
	result func(*rpc.Client, int) error) error {
	home := os.Getenv("HOME")
	if home == "" {
		return errors.New("HOME is not defined")
	}

	socketPath := filepath.Join(home, ".ccloudvm/socket")
	client, err := rpc.DialHTTP("unix", socketPath)
	if err != nil {
		return errors.Wrap(err, "Unable to communicate with server")
	}

	id, err := call(client)
	if err != nil {
		return err
	}

	ch := make(chan error)
	go func() {
		ch <- result(client, id)
	}()

	reply := struct{}{}

	select {
	case err = <-ch:
		return err
	case <-ctx.Done():
		_ = client.Call("ServerAPI.Cancel", id, &reply)
		return <-ch
	}
}

func getProxy(upper, lower string) (string, error) {
	proxy := os.Getenv(upper)
	if proxy == "" {
		proxy = os.Getenv(lower)
	}

	if proxy == "" {
		return "", nil
	}

	if proxy[len(proxy)-1] == '/' {
		proxy = proxy[:len(proxy)-1]
	}

	proxyURL, err := url.Parse(proxy)
	if err != nil {
		return "", errors.Wrapf(err, "Failed to parse %s", proxy)
	}
	return proxyURL.String(), nil
}

// Create sets up the VM
func Create(ctx context.Context, workloadName string, debug bool, update bool, customSpec *types.VMSpec) error {
	HTTPProxy, err := getProxy("HTTP_PROXY", "http_proxy")
	if err != nil {
		return err
	}

	HTTPSProxy, err := getProxy("HTTPS_PROXY", "https_proxy")
	if err != nil {
		return err
	}

	if HTTPSProxy != "" {
		u, _ := url.Parse(HTTPSProxy)
		u.Scheme = "http"
		HTTPSProxy = u.String()
	}

	noProxy := os.Getenv("no_proxy")

	return issueCommand(ctx,
		func(client *rpc.Client) (int, error) {
			var id int
			err := client.Call("ServerAPI.Create",
				types.CreateArgs{
					WorkloadName: workloadName,
					Debug:        debug,
					Update:       update,
					CustomSpec:   *customSpec,
					HTTPProxy:    HTTPProxy,
					HTTPSProxy:   HTTPSProxy,
					NoProxy:      noProxy,
				},
				&id)
			return id, err
		},
		func(client *rpc.Client, id int) error {
			var result types.CreateResult
			for {
				err := client.Call("ServerAPI.CreateResult", id, &result)
				if result.Finished || err != nil {
					return err
				}
				fmt.Print(result.Line)
			}
		})
}

// Start launches the VM
func Start(ctx context.Context, customSpec *types.VMSpec) error {
	return issueCommand(ctx,
		func(client *rpc.Client) (int, error) {
			var id int
			err := client.Call("ServerAPI.Start", *customSpec, &id)
			return id, err
		},
		func(client *rpc.Client, id int) error {
			var result struct{}
			return client.Call("ServerAPI.StartResult", id, &result)
		})
}

// Stop requests the VM shuts down cleanly
func Stop(ctx context.Context) error {
	return issueCommand(ctx,
		func(client *rpc.Client) (int, error) {
			var id int
			err := client.Call("ServerAPI.Stop", struct{}{}, &id)
			return id, err
		},
		func(client *rpc.Client, id int) error {
			var result struct{}
			return client.Call("ServerAPI.StopResult", id, &result)
		})
}

// Quit forceably kills VM
func Quit(ctx context.Context) error {
	return issueCommand(ctx,
		func(client *rpc.Client) (int, error) {
			var id int
			err := client.Call("ServerAPI.Quit", struct{}{}, &id)
			return id, err
		},
		func(client *rpc.Client, id int) error {
			var result struct{}
			return client.Call("ServerAPI.QuitResult", id, &result)
		})
}

func sshReady(ctx context.Context, sshPort int) bool {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp",
		fmt.Sprintf("127.0.0.1:%d", sshPort))
	if err != nil {
		return false
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Millisecond * 500))
	scanner := bufio.NewScanner(conn)
	retval := scanner.Scan()
	_ = conn.Close()
	return retval
}

func statusVM(ctx context.Context, keyPath, workloadName string, sshPort int, qemuport uint) {
	status := "VM down"
	ssh := "N/A"
	if sshReady(ctx, sshPort) {
		status = "VM up"
		ssh = fmt.Sprintf("ssh -q -F /dev/null -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -o IdentitiesOnly=yes -i %s 127.0.0.1 -p %d", keyPath, sshPort)
	}

	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 0, 8, 0, '\t', 0)
	fmt.Fprintf(w, "Workload\t:\t%s\n", workloadName)
	fmt.Fprintf(w, "Status\t:\t%s\n", status)
	fmt.Fprintf(w, "SSH\t:\t%s\n", ssh)
	if qemuport != 0 {
		fmt.Fprintf(w, "QEMU Debug Port\t:\t%d\n", qemuport)
	}
	_ = w.Flush()
}

func waitForSSH(ctx context.Context, in *types.InstanceDetails, silent bool) error {
	if !sshReady(ctx, in.SSH.Port) {
		if !silent {
			fmt.Printf("Waiting for VM to boot ")
		}
	DONE:
		for {
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return fmt.Errorf("Cancelled")
			}

			if sshReady(ctx, in.SSH.Port) {
				break DONE
			}

			if !silent {
				fmt.Print(".")
			}
		}
		if !silent {
			fmt.Println()
		}
	}

	return nil
}

// Status prints out VM information
func Status(ctx context.Context) error {
	return issueCommand(ctx,
		func(client *rpc.Client) (int, error) {
			var id int
			err := client.Call("ServerAPI.GetInstanceDetails", struct{}{}, &id)
			return id, err
		},
		func(client *rpc.Client, id int) error {
			var result types.InstanceDetails
			err := client.Call("ServerAPI.GetInstanceDetailsResult", id, &result)
			if err != nil {
				return err
			}
			statusVM(ctx, result.SSH.KeyPath, result.Workload, result.SSH.Port,
				result.DebugPort)
			return nil
		})
}

// Run connects to the VM via SSH and runs the desired command
func Run(ctx context.Context, command string) error {
	path, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("Unable to locate ssh binary")
	}

	var result types.InstanceDetails
	err = issueCommand(ctx,
		func(client *rpc.Client) (int, error) {
			var id int
			err := client.Call("ServerAPI.GetInstanceDetails", struct{}{}, &id)
			return id, err
		},
		func(client *rpc.Client, id int) error {
			err := client.Call("ServerAPI.GetInstanceDetailsResult", id, &result)
			if err != nil {
				return err
			}
			return nil
		})

	if err != nil {
		return err
	}

	err = waitForSSH(ctx, &result, command != "")
	if err != nil {
		return err
	}

	args := []string{
		path,
		"-q", "-F", "/dev/null",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "StrictHostKeyChecking=no",
		"-o", "IdentitiesOnly=yes",
		"-i", result.SSH.KeyPath,
		"127.0.0.1", "-p", strconv.Itoa(result.SSH.Port),
	}

	if command != "" {
		args = append(args, command)
	}

	err = syscall.Exec(path, args, os.Environ())
	return err
}

// Connect opens a shell to the VM via
func Connect(ctx context.Context) error {
	return Run(ctx, "")
}

// Delete the VM
func Delete(ctx context.Context) error {
	return issueCommand(ctx,
		func(client *rpc.Client) (int, error) {
			var id int
			err := client.Call("ServerAPI.Delete", struct{}{}, &id)
			return id, err
		},
		func(client *rpc.Client, id int) error {
			var result struct{}
			return client.Call("ServerAPI.DeleteResult", id, &result)
		})
}
