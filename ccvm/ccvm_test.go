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

package main

import (
	"bufio"
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/intel/ccloudvm/types"
	"github.com/pkg/errors"
)

const standardTimeout = time.Second * 300

func setupDownloadDir(t *testing.T) (string, string) {
	home := os.Getenv("HOME")
	if home == "" {
		t.Fatalf("Unable to determine home directory")
	}

	tmpDir, err := ioutil.TempDir(home, "ccloudvmtest-")
	if err != nil {
		t.Fatalf("Unable to create temporary directory: %v", err)
	}

	downDir := filepath.Join(tmpDir, "down")
	err = os.Mkdir(downDir, 0755)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("Unable to create download directory %s: %v", downDir, err)
	}

	return tmpDir, downDir
}

func waitForSSH(ctx context.Context, sshPort int) error {
	for {
		dialer := net.Dialer{}
		conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", sshPort))
		if err == nil {
			_ = conn.SetReadDeadline(time.Now().Add(time.Millisecond * 500))
			scanner := bufio.NewScanner(conn)
			retval := scanner.Scan()
			_ = conn.Close()
			if retval {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
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

func setProxies(args *types.CreateArgs) error {
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

	args.HTTPProxy = HTTPProxy
	args.HTTPSProxy = HTTPSProxy
	args.NoProxy = noProxy

	return nil
}

func createDownloader() (*downloader, error) {
	d := &downloader{}
	home := os.Getenv("HOME")
	if home == "" {
		return nil, errors.New("Unable to determine home directory")
	}

	err := d.setup(filepath.Join(home, ".ccloudvm"))
	if err != nil {
		return nil, err
	}

	return d, nil
}

func TestSystem(t *testing.T) {
	ctx, cancelFunc := context.WithTimeout(context.Background(), standardTimeout)
	defer func() {
		cancelFunc()
	}()

	// Create a directory which will be mounted inside the newly created
	// instance.  This directory will contain a sub directory called down
	// into which the instance will download the README.md file of this
	// project.  At the end of the test we check for the existence of
	// this file on the host, checking download and mounting.  This test
	// requires the cooperation of the semaphore.yaml workload.

	tmpDir, downDir := setupDownloadDir(t)
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	vmSpec := &types.VMSpec{
		MemMiB: 1024,
		CPUs:   1,
		Mounts: []types.Mount{
			{
				Tag:           "tmpdir",
				SecurityModel: "passthrough",
				Path:          tmpDir,
			},
		},
		PortMappings: []types.PortMapping{
			{
				Host:  10022,
				Guest: 22,
			},
		},
	}

	name := makeRandomName() + "-test"
	createArgs := &types.CreateArgs{
		Name:         name,
		WorkloadName: "semaphore",
		CustomSpec:   *vmSpec,
		Debug:        true,
	}
	err := setProxies(createArgs)
	if err != nil {
		t.Fatalf("Unable to set proxies: %v", err)
	}

	resultCh := make(chan interface{})
	go func() {
		for r := range resultCh {
			fmt.Print(r)
		}
	}()

	d, err := createDownloader()
	if err != nil {
		t.Fatalf("Unable to create downloaded:  %v", err)
	}
	downloadCh := make(chan downloadRequest)
	doneCh := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		d.start(doneCh, downloadCh)
		wg.Done()
	}()

	err = createInstance(ctx, resultCh, downloadCh, createArgs)
	close(resultCh)
	if err != nil {
		t.Fatalf("Unable to create VM: %v", err)
	}
	close(doneCh)
	wg.Wait()

	err = start(ctx, name, vmSpec)
	if err == nil || err == context.DeadlineExceeded {
		t.Errorf("Start expected to fail")
	}

	err = stop(ctx, name)
	if err != nil {
		t.Errorf("Failed to Stop instance: %v", err)
	}

	err = start(ctx, name, vmSpec)
	if err != nil {
		t.Errorf("Failed to Restart instance: %v", err)
	}

	port, err := vmSpec.SSHPort()
	if err != nil {
		t.Errorf("Unable to determine SSH port of instance: %v", err)
	}

	err = waitForSSH(ctx, port)
	if err != nil {
		t.Errorf("Instance is not available via SSH: %v", err)
	}

	err = quit(ctx, name)
	if err != nil {
		t.Errorf("Failed to Restart instance: %v", err)
	}

	err = deleteInstance(context.Background(), name)
	if err != nil {
		t.Errorf("Failed to Delete instance: %v", err)
	}

	readmePath := filepath.Join(downDir, "README.md")
	_, err = os.Stat(readmePath)
	if err != nil {
		t.Errorf("Expected %s to exist: %v", readmePath, err)
	}
}
