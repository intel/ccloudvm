//
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
//

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/intel/ccloudvm/types"
	"github.com/intel/govmm/qemu"
	"github.com/pkg/errors"
)

// Download Parameters

const (
	friendlyNameParam = "name"
	urlParam          = "url"
)

func bootVM(ctx context.Context, ws *workspace, in *types.VMSpec) error {
	disconnectedCh := make(chan struct{})
	socket := path.Join(ws.instanceDir, "socket")
	qmp, _, err := qemu.QMPStart(ctx, socket, qemu.QMPConfig{}, disconnectedCh)
	if err == nil {
		qmp.Shutdown()
		return fmt.Errorf("VM is already running")
	}

	vmImage := path.Join(ws.instanceDir, "image.qcow2")
	isoPath := path.Join(ws.instanceDir, "config.iso")
	memParam := fmt.Sprintf("%dM", in.MemMiB)
	CPUsParam := fmt.Sprintf("cpus=%d", in.CPUs)
	args := []string{
		"-qmp", fmt.Sprintf("unix:%s,server,nowait", socket),
		"-m", memParam, "-smp", CPUsParam,
		"-drive", fmt.Sprintf("file=%s,if=virtio,aio=threads,format=qcow2", vmImage),
		"-drive", fmt.Sprintf("file=%s,if=virtio,media=cdrom", isoPath),
		"-daemonize", "-enable-kvm", "-cpu", "host",
		"-net", "nic,model=virtio",
		"-device", "virtio-rng-pci",
	}

	for i, m := range in.Mounts {
		fsdevParam := fmt.Sprintf("local,security_model=%s,id=fsdev%d,path=%s",
			m.SecurityModel, i, m.Path)
		devParam := fmt.Sprintf("virtio-9p-pci,id=fs%[1]d,fsdev=fsdev%[1]d,mount_tag=%s",
			i, m.Tag)
		args = append(args, "-fsdev", fsdevParam, "-device", devParam)
	}

	for _, d := range in.Drives {
		options := strings.TrimSpace(d.Options)
		if options != "" {
			options = "," + options
		}
		driveParam := fmt.Sprintf("file=%s,if=virtio,format=%s%s", d.Path,
			d.Format, options)
		args = append(args, "-drive", driveParam)
	}

	var b bytes.Buffer
	if len(in.PortMappings) > 0 {
		i := 0
		p := in.PortMappings[i]
		b.WriteString(fmt.Sprintf("user,hostfwd=tcp::%d-:%d", p.Host, p.Guest))
		for i = i + 1; i < len(in.PortMappings); i++ {
			p := in.PortMappings[i]
			b.WriteString(fmt.Sprintf(",hostfwd=tcp::%d-:%d", p.Host, p.Guest))
		}
	}

	netParam := b.String()
	if len(netParam) > 0 {
		args = append(args, "-net", netParam)
	}

	if in.Qemuport != 0 {
		args = append(args, "-chardev",
			fmt.Sprintf("socket,host=localhost,port=%d,id=ccld0,server,nowait", in.Qemuport),
			"-device", "isa-serial,chardev=ccld0")
	}

	args = append(args, "-display", "none", "-vga", "none")

	output, err := qemu.LaunchCustomQemu(ctx, "", args, nil, nil, nil)
	if err != nil {
		return fmt.Errorf("Failed to launch qemu : %v, %s", err, output)
	}
	return nil
}

func executeQMPCommand(ctx context.Context, instanceDir string,
	cmd func(ctx context.Context, q *qemu.QMP) error) error {
	socket := path.Join(instanceDir, "socket")
	disconnectedCh := make(chan struct{})
	qmp, _, err := qemu.QMPStart(ctx, socket, qemu.QMPConfig{}, disconnectedCh)
	if err != nil {
		return errors.Wrap(err, "Failed to connect to VM")
	}
	defer qmp.Shutdown()

	err = qmp.ExecuteQMPCapabilities(ctx)
	if err != nil {
		return errors.Wrap(err, "Unable to query QEMU caps")
	}

	err = cmd(ctx, qmp)
	if err != nil {
		return errors.Wrap(err, "Unable to execute vm command")
	}

	return nil
}

func stopVM(ctx context.Context, instanceDir string) error {
	return executeQMPCommand(ctx, instanceDir, func(ctx context.Context, q *qemu.QMP) error {
		return q.ExecuteSystemPowerdown(ctx)
	})
}

func quitVM(ctx context.Context, instanceDir string) error {
	return executeQMPCommand(ctx, instanceDir, func(ctx context.Context, q *qemu.QMP) error {
		return q.ExecuteQuit(ctx)
	})
}

func serveLocalFile(ctx context.Context, downloadCh chan<- downloadRequest, transport *http.Transport,
	w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	URL := params.Get(urlParam)

	path, err := downloadFile(ctx, downloadCh, transport, URL, func(progress) {})
	if err != nil {
		// May not be the correct error code but the error message is only going
		// to end up in cloud-init's logs.
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	f, err := os.Open(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, err = io.Copy(w, f)
	_ = f.Close()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to copy %s : %v", friendlyNameParam, err),
			http.StatusInternalServerError)
		return
	}
}

func startHTTPServer(ctx context.Context, resultCh chan interface{}, downloadCh chan<- downloadRequest,
	transport *http.Transport, listener net.Listener, errCh chan error) {
	finished := false
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var b bytes.Buffer
		_, err := io.Copy(&b, r.Body)
		if err != nil {
			// TODO: Figure out what to do here
			return
		}
		line := string(b.Bytes())
		if line == "FINISHED" {
			finished = true
			_ = listener.Close()
			return
		}
		if line == "OK" || line == "FAIL" {
			resultCh <- types.CreateResult{
				Line: fmt.Sprintf("[%s]\n", line),
			}
		} else {
			resultCh <- types.CreateResult{
				Line: fmt.Sprintf("%s : ", line),
			}
		}
	})

	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		serveLocalFile(ctx, downloadCh, transport, w, r)
	})

	server := &http.Server{
		Handler: mux,
	}
	go func() {
		_ = server.Serve(listener)
		if finished {
			errCh <- nil
		} else {
			errCh <- fmt.Errorf("HTTP server exited prematurely")
		}
	}()
}

func createLocalListener() (net.Listener, int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, errors.Wrap(err, "Unable to create listener")
	}

	_, portStr, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		return nil, 0, errors.Wrap(err, "Unable to parse address of local HTTP server")
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		_ = listener.Close()
		return nil, 0, errors.Wrap(err, "Unable to determine port number of local HTTP server")
	}

	return listener, port, nil
}

func manageInstallation(ctx context.Context, resultCh chan interface{},
	downloadCh chan<- downloadRequest, transport *http.Transport, listener net.Listener,
	instanceDir string) error {
	socket := path.Join(instanceDir, "socket")
	disconnectedCh := make(chan struct{})

	qmp, _, err := qemu.QMPStart(ctx, socket, qemu.QMPConfig{}, disconnectedCh)
	if err != nil {
		_ = listener.Close()
		return errors.Wrap(err, "Unable to connect to VM")
	}

	qemuShutdown := true
	defer func() {
		if qemuShutdown {
			ctx, cancelFn := context.WithTimeout(context.Background(), time.Second)
			_ = qmp.ExecuteQuit(ctx)
			<-disconnectedCh
			cancelFn()
		}
		qmp.Shutdown()
	}()

	err = qmp.ExecuteQMPCapabilities(ctx)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("Unable to query QEMU caps")
	}

	errCh := make(chan error)
	startHTTPServer(ctx, resultCh, downloadCh, transport, listener, errCh)
	select {
	case <-ctx.Done():
		_ = listener.Close()
		<-errCh
		return ctx.Err()
	case err := <-errCh:
		if err == nil {
			qemuShutdown = false
		}
		return err
	case <-disconnectedCh:
		qemuShutdown = false
		_ = listener.Close()
		<-errCh
		return fmt.Errorf("Lost connection to QEMU instance")
	}
}
