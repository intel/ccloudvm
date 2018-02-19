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
	"io"
	"io/ioutil"
	"net"
	"net/http"
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

func getGoPath() (string, error) {
	goPathBytes, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		return "", errors.Wrap(err, "Unable to determine GOPATH")
	}
	return strings.TrimSpace(string(goPathBytes)), nil
}

// Setup Installs dependencies
func Setup(ctx context.Context) error {
	home := os.Getenv("HOME")
	if home == "" {
		return errors.New("HOME is not defined")
	}
	home = strings.TrimSpace(home)

	goPath, err := getGoPath()
	if err != nil {
		return err
	}

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

// Teardown disables the ccloudvm service and deletes all existing instances
func Teardown(ctx context.Context) error {
	home := os.Getenv("HOME")
	if home == "" {
		return errors.New("HOME is not defined")
	}

	var instances []string
	err := issueCommand(ctx,
		func(client *rpc.Client) (int, error) {
			var id int
			err := client.Call("ServerAPI.GetInstances", struct{}{}, &id)
			return id, err
		},
		func(client *rpc.Client, id int) error {
			return client.Call("ServerAPI.GetInstancesResult", id, &instances)
		})
	if err == nil {
		for _, i := range instances {
			fmt.Printf("Deleting %s\n", i)
			_ = Delete(ctx, i)
		}
	}

	/* There's a tiny chance of a race here.  If someone started creating
	   a VM in one window and called teardown in another and the instance came into
	   existence after we called GetInstances but before we shut down the service,
	   there's a small chance the instance could be leaked. */

	fmt.Println("Removing ccloudvm service")

	_ = exec.Command("systemctl", "--user", "disable", "ccloudvm.service").Run()
	_ = exec.Command("systemctl", "--user", "disable", "ccloudvm.socket").Run()
	_ = exec.Command("systemctl", "--user", "stop", "ccloudvm.service").Run()
	_ = exec.Command("systemctl", "--user", "stop", "ccloudvm.socket").Run()

	systemdRootPath := filepath.Join(home, ".local/share/systemd/user")

	servicePath := filepath.Join(systemdRootPath, "ccloudvm.service")
	_ = os.Remove(servicePath)

	socketPath := filepath.Join(systemdRootPath, "ccloudvm.socket")
	_ = os.Remove(socketPath)
	return nil
}

func dialHTTP(ctx context.Context, socketPath string) (client *rpc.Client, err error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()
	d := &net.Dialer{}
	conn, err := d.DialContext(timeoutCtx, "unix", socketPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if client == nil {
			_ = conn.Close()
		}
	}()
	connectString := fmt.Sprintf("CONNECT %s HTTP/1.0\n\n", rpc.DefaultRPCPath)
	_, err = io.WriteString(conn, connectString)
	if err != nil {
		return nil, err
	}

	r, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{
		Method: http.MethodConnect,
	})
	if err != nil {
		return nil, err
	}

	if r.Status != "200 Connected to Go RPC" {
		return nil, errors.Errorf("Unexpected response (%s) from RPC server", r.Status)
	}

	return rpc.NewClient(conn), nil
}

func issueCommand(ctx context.Context, call func(*rpc.Client) (int, error),
	result func(*rpc.Client, int) error) error {
	home := os.Getenv("HOME")
	if home == "" {
		return errors.New("HOME is not defined")
	}

	socketPath := filepath.Join(home, ".ccloudvm/socket")
	client, err := dialHTTP(ctx, socketPath)
	if err != nil {
		err2 := exec.Command("systemctl", "--user", "restart", "ccloudvm.socket").Run()
		if err2 != nil {
			return errors.Wrap(err, "Unable to communicate with server. Try running 'ccloudvm setup'")
		}
		client, err = rpc.DialHTTP("unix", socketPath)
		if err != nil {
			return errors.Wrap(err, "Unable to communicate with server")
		}
	}

	defer func() {
		_ = client.Close()
	}()

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
func Create(ctx context.Context, instanceName, workloadName string, debug bool, update bool, customSpec *types.VMSpec) error {
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

	noProxy := os.Getenv("NO_PROXY")
	if noProxy == "" {
		noProxy = os.Getenv("no_proxy")
	}

	goPath, err := getGoPath()
	if err != nil {
		return err
	}

	return issueCommand(ctx,
		func(client *rpc.Client) (int, error) {
			var id int
			err := client.Call("ServerAPI.Create",
				types.CreateArgs{
					Name:         instanceName,
					WorkloadName: workloadName,
					Debug:        debug,
					Update:       update,
					CustomSpec:   *customSpec,
					HTTPProxy:    HTTPProxy,
					HTTPSProxy:   HTTPSProxy,
					NoProxy:      noProxy,
					GoPath:       goPath,
				},
				&id)
			return id, err
		},
		func(client *rpc.Client, id int) error {
			var result types.CreateResult
			for {
				err := client.Call("ServerAPI.CreateResult", id, &result)
				if err != nil {
					return err
				}
				if result.Finished {
					fmt.Printf("\nInstance %s created\n", result.Name)
					fmt.Printf("Type 'ccloudvm connect %s' to start using it.\n", result.Name)
					return nil
				}
				fmt.Print(result.Line)
			}
		})
}

// Start launches the VM
func Start(ctx context.Context, instanceName string, customSpec *types.VMSpec) error {
	return issueCommand(ctx,
		func(client *rpc.Client) (int, error) {
			var id int
			err := client.Call("ServerAPI.Start", types.StartArgs{
				Name:   instanceName,
				VMSpec: *customSpec,
			}, &id)
			return id, err
		},
		func(client *rpc.Client, id int) error {
			var result struct{}
			return client.Call("ServerAPI.StartResult", id, &result)
		})
}

// Stop requests the VM shuts down cleanly
func Stop(ctx context.Context, instanceName string) error {
	return issueCommand(ctx,
		func(client *rpc.Client) (int, error) {
			var id int
			err := client.Call("ServerAPI.Stop", instanceName, &id)
			return id, err
		},
		func(client *rpc.Client, id int) error {
			var result struct{}
			return client.Call("ServerAPI.StopResult", id, &result)
		})
}

// Quit forceably kills VM
func Quit(ctx context.Context, instanceName string) error {
	return issueCommand(ctx,
		func(client *rpc.Client) (int, error) {
			var id int
			err := client.Call("ServerAPI.Quit", instanceName, &id)
			return id, err
		},
		func(client *rpc.Client, id int) error {
			var result struct{}
			return client.Call("ServerAPI.QuitResult", id, &result)
		})
}

func sshReady(ctx context.Context, hostIP net.IP, sshPort int) bool {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp",
		fmt.Sprintf("%s:%d", hostIP, sshPort))
	if err != nil {
		return false
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Millisecond * 500))
	scanner := bufio.NewScanner(conn)
	retval := scanner.Scan()
	_ = conn.Close()
	return retval
}

func sshConnectionString(details *types.InstanceDetails) string {
	return fmt.Sprintf("ssh -q -F /dev/null -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -o IdentitiesOnly=yes -i %s %s -p %d",
		details.SSH.KeyPath, details.VMSpec.HostIP, details.SSH.Port)
}

func statusVM(ctx context.Context, details *types.InstanceDetails) {
	status := "VM down"
	ssh := "N/A"
	if sshReady(ctx, details.VMSpec.HostIP, details.SSH.Port) {
		status = "VM up"
		ssh = sshConnectionString(details)
	}

	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 0, 8, 0, '\t', 0)
	fmt.Fprintf(w, "Name\t:\t%s\n", details.Name)
	fmt.Fprintf(w, "HostIP\t:\t%s\n", details.VMSpec.HostIP)
	fmt.Fprintf(w, "Workload\t:\t%s\n", details.Workload)
	fmt.Fprintf(w, "Status\t:\t%s\n", status)
	fmt.Fprintf(w, "SSH\t:\t%s\n", ssh)
	fmt.Fprintf(w, "VCPUs\t:\t%d\n", details.VMSpec.CPUs)
	fmt.Fprintf(w, "Mem\t:\t%d MiB\n", details.VMSpec.MemMiB)
	fmt.Fprintf(w, "Disk\t:\t%d GiB\n", details.VMSpec.DiskGiB)
	if details.VMSpec.Qemuport != 0 {
		fmt.Fprintf(w, "QEMU Debug Port\t:\t%d\n", details.VMSpec.Qemuport)
	}
	_ = w.Flush()
}

func waitForSSH(ctx context.Context, in *types.InstanceDetails, silent bool) error {
	if !sshReady(ctx, in.VMSpec.HostIP, in.SSH.Port) {
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

			if sshReady(ctx, in.VMSpec.HostIP, in.SSH.Port) {
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
func Status(ctx context.Context, instanceName string) error {
	return issueCommand(ctx,
		func(client *rpc.Client) (int, error) {
			var id int
			err := client.Call("ServerAPI.GetInstanceDetails", instanceName, &id)
			return id, err
		},
		func(client *rpc.Client, id int) error {
			var result types.InstanceDetails
			err := client.Call("ServerAPI.GetInstanceDetailsResult", id, &result)
			if err != nil {
				return err
			}
			statusVM(ctx, &result)
			return nil
		})
}

// Run connects to the VM via SSH and runs the desired command
func Run(ctx context.Context, instanceName, command string) error {
	path, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("Unable to locate ssh binary")
	}

	var result types.InstanceDetails
	err = issueCommand(ctx,
		func(client *rpc.Client) (int, error) {
			var id int
			err := client.Call("ServerAPI.GetInstanceDetails", instanceName, &id)
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
		result.VMSpec.HostIP.String(), "-p", strconv.Itoa(result.SSH.Port),
	}

	if command != "" {
		args = append(args, command)
	}

	err = syscall.Exec(path, args, os.Environ())
	return err
}

// Connect opens a shell to the VM via
func Connect(ctx context.Context, instanceName string) error {
	return Run(ctx, instanceName, "")
}

// Delete the VM
func Delete(ctx context.Context, instanceName string) error {
	return issueCommand(ctx,
		func(client *rpc.Client) (int, error) {
			var id int
			err := client.Call("ServerAPI.Delete", instanceName, &id)
			return id, err
		},
		func(client *rpc.Client, id int) error {
			var result struct{}
			return client.Call("ServerAPI.DeleteResult", id, &result)
		})
}

// Instances provides information about all of the current instances
func Instances(ctx context.Context) error {
	var instances []string
	err := issueCommand(ctx,
		func(client *rpc.Client) (int, error) {
			var id int
			err := client.Call("ServerAPI.GetInstances", struct{}{}, &id)
			return id, err
		},
		func(client *rpc.Client, id int) error {
			return client.Call("ServerAPI.GetInstancesResult", id, &instances)
		})

	if err != nil {
		return err
	}

	instanceDetails := make([]types.InstanceDetails, 0, len(instances))
	for _, name := range instances {
		_ = issueCommand(ctx,
			func(client *rpc.Client) (int, error) {
				var id int
				err := client.Call("ServerAPI.GetInstanceDetails", name, &id)
				return id, err
			},
			func(client *rpc.Client, id int) error {
				var details types.InstanceDetails
				err := client.Call("ServerAPI.GetInstanceDetailsResult", id, &details)
				if err != nil {
					return err
				}
				instanceDetails = append(instanceDetails, details)
				return nil
			})
	}

	if len(instanceDetails) == 0 {
		return nil
	}

	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 0, 8, 1, '\t', 0)
	fmt.Fprintln(w, "Name\tHostIP\tWorkload\tVCPUs\tMem\tDisk\t")
	for _, id := range instanceDetails {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d MiB\t%d Gib\n",
			id.Name, id.VMSpec.HostIP, id.Workload,
			id.VMSpec.CPUs, id.VMSpec.MemMiB, id.VMSpec.DiskGiB)
	}
	_ = w.Flush()

	return nil

}
