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

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/net/http/httpproxy"
)

type progressCB func(p progress)

type progress struct {
	downloadedMB int
	totalMB      int
	complete     bool
}

type downloadUpdate struct {
	p    progress
	err  error
	path string
}

type updateInfo struct {
	p    progress
	err  error
	name string
}

type progressReader struct {
	downloaded int64
	totalMB    int
	reader     io.Reader
	progressCh chan updateInfo
	name       string
}

type downloadRequest struct {
	progress  chan downloadUpdate
	URL       string
	ctx       context.Context
	transport *http.Transport
}

type downloadedFile struct {
	ctx       context.Context
	cancel    context.CancelFunc
	p         progress
	listeners []downloadRequest
	path      string
}

type downloader struct {
	files    map[string]*downloadedFile
	cacheDir string
}

func (pr *progressReader) Read(p []byte) (int, error) {
	read, err := pr.reader.Read(p)
	if err == nil {
		oldMB := pr.downloaded / 10000000
		pr.downloaded += int64(read)
		newMB := pr.downloaded / 10000000
		if newMB > oldMB {
			pr.progressCh <- updateInfo{
				p: progress{
					downloadedMB: int(newMB * 10),
					totalMB:      pr.totalMB,
				},
				name: pr.name,
			}
		}
	}
	return read, err
}

func getHTTPTransport(HTTPProxy, HTTPSProxy, NoProxy string) *http.Transport {
	proxyCfg := httpproxy.Config{
		HTTPProxy:  HTTPProxy,
		HTTPSProxy: HTTPSProxy,
		NoProxy:    NoProxy,
	}
	proxyFunc := proxyCfg.ProxyFunc()
	defaultTransport := http.DefaultTransport.(*http.Transport)

	return &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			return proxyFunc(req.URL)
		},
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:          defaultTransport.MaxIdleConns,
		IdleConnTimeout:       defaultTransport.IdleConnTimeout,
		TLSHandshakeTimeout:   defaultTransport.TLSHandshakeTimeout,
		ExpectContinueTimeout: defaultTransport.ExpectContinueTimeout,
	}
}

func makeFileName(URL string) (string, error) {
	u, err := url.Parse(URL)
	if err != nil {
		return "", fmt.Errorf("unable to parse %s:%v", err, URL)
	}
	return filepath.Base(u.Path), nil
}

func getFile(ctx context.Context, name, URL string, transport *http.Transport,
	dest io.WriteCloser, progressCh chan updateInfo) (err error) {
	defer func() {
		err1 := dest.Close()
		if err == nil && err1 != nil {
			err = err1
		}
	}()
	req, err := http.NewRequest("GET", URL, nil)
	if err != nil {
		return
	}
	req = req.WithContext(ctx)
	cli := &http.Client{
		Transport: transport,
	}
	resp, err := cli.Do(req)
	if err != nil {
		return
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("Failed to download %s : %s", URL, resp.Status)
		return
	}

	pr := &progressReader{
		reader:     resp.Body,
		progressCh: progressCh,
		name:       name,
	}
	if resp.ContentLength == -1 {
		pr.totalMB = -1
	} else {
		pr.totalMB = int(resp.ContentLength / 1000000)
	}

	buf := make([]byte, 1<<20)
	_, err = io.CopyBuffer(dest, pr, buf)

	if err == nil {
		progressCh <- updateInfo{
			p: progress{
				downloadedMB: pr.totalMB,
				totalMB:      pr.totalMB,
				complete:     true,
			},
			name: name,
		}
	}

	return
}

func prepareDownload(ctx context.Context, imgPath, name, URL string,
	transport *http.Transport, progressCh chan updateInfo) error {
	tmpImgPath := imgPath + ".part"

	if _, err := os.Stat(tmpImgPath); err == nil {
		_ = os.Remove(tmpImgPath)
	}

	f, err := os.Create(tmpImgPath)
	if err != nil {
		return errors.Wrap(err, "Unable to create download file")
	}

	err = getFile(ctx, name, URL, transport, f, progressCh)
	if err != nil {
		_ = os.Remove(tmpImgPath)
		return errors.Wrapf(err, "Unable download file %s", URL)
	}

	err = os.Rename(tmpImgPath, imgPath)
	if err != nil {
		_ = os.Remove(tmpImgPath)
		return errors.Wrapf(err, "Unable move downloaded file to %s: %v", imgPath)
	}

	return nil
}

func initiateDownload(ctx context.Context, progressCh chan updateInfo, imgPath, name, URL string,
	transport *http.Transport, wg *sync.WaitGroup) {
	fmt.Printf("First download of %s\n", URL)
	if err := prepareDownload(ctx, imgPath, name, URL, transport, progressCh); err != nil {
		progressCh <- updateInfo{
			err: err,
			p: progress{
				complete: true,
			},
			name: name,
		}
	}
	wg.Done()
}

func (d *downloader) setup(ccvmDir string) error {
	d.files = make(map[string]*downloadedFile)

	d.cacheDir = path.Join(ccvmDir, "cache")
	if err := os.MkdirAll(d.cacheDir, 0755); err != nil {
		return errors.Wrapf(err, "Unable to create directory %s", d.cacheDir)
	}

	_ = filepath.Walk(d.cacheDir, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}

		fullPath := filepath.Join(d.cacheDir, info.Name())
		if filepath.Ext(info.Name()) == ".part" {
			fmt.Printf("Discarding partially downloaded file %s", fullPath)
			_ = os.Remove(fullPath)
			return nil
		}
		size := int(info.Size() / (1000 * 1000))
		d.files[info.Name()] = &downloadedFile{
			p: progress{
				complete:     true,
				downloadedMB: size,
				totalMB:      size,
			},
			path: filepath.Join(d.cacheDir, info.Name()),
		}
		fmt.Printf("Found cached file %s %dMB\n", fullPath, size)

		return nil
	})

	return nil
}

func (d *downloader) activeDownloads() bool {
	active := 0
	for _, f := range d.files {
		if len(f.listeners) > 0 {
			f.cancel()
			active++
		}
	}

	return active > 0
}

func processUpdate(df *downloadedFile, u updateInfo) {
	df.p = u.p

	if u.p.complete {
		fmt.Printf("Download of %s finished\n", u.name)
	}
	listeners := make([]downloadRequest, 0, len(df.listeners))
	for _, l := range df.listeners {
		select {
		case <-l.ctx.Done():
			var err error
			if !df.p.complete {
				err = errors.New("Download request cancelled")
			}
			l.progress <- downloadUpdate{
				p:    df.p,
				err:  err,
				path: df.path,
			}
			close(l.progress)
		default:
			l.progress <- downloadUpdate{
				p:    df.p,
				err:  u.err,
				path: df.path,
			}
			if df.p.complete {
				close(l.progress)
			} else {
				listeners = append(listeners, l)
			}
		}
	}

	df.listeners = listeners
	if len(df.listeners) == 0 || df.p.complete {
		if !df.p.complete {
			fmt.Printf("Download of %s cancelled due to lack of interested clients\n",
				u.name)
		}
		df.cancel()
	}
}

/* TODO requestCH is a global.  It needs to go */
func (d *downloader) start(doneCh <-chan struct{}, requestCh chan downloadRequest) {
	shuttingDown := false
	progressCh := make(chan updateInfo)

	var wg sync.WaitGroup

DONE:
	for {
		select {
		case <-doneCh:
			if !d.activeDownloads() {
				break DONE
			} else {
				shuttingDown = true
				doneCh = nil
			}
		case r := <-requestCh:
			if shuttingDown {
				r.progress <- downloadUpdate{
					err: errors.New("Download manager shutting down"),
				}
				close(r.progress)
				continue
			}
			name, err := makeFileName(r.URL)
			if err != nil {
				r.progress <- downloadUpdate{
					err: nil,
				}
				close(r.progress)
				continue
			}
			df, ok := d.files[name]
			if ok {
				if df.p.complete {
					fmt.Printf("Download of %s finished\n", name)
					r.progress <- downloadUpdate{
						p:    df.p,
						err:  nil,
						path: df.path,
					}
					close(r.progress)
				} else {
					df.listeners = append(df.listeners, r)
				}
				continue
			}
			ctx, cancel := context.WithCancel(context.Background())
			imgPath := filepath.Join(d.cacheDir, name)
			d.files[name] = &downloadedFile{
				listeners: []downloadRequest{
					r,
				},
				ctx:    ctx,
				cancel: cancel,
				path:   imgPath,
			}
			wg.Add(1)
			go initiateDownload(ctx, progressCh, imgPath, name, r.URL, r.transport, &wg)
		case u := <-progressCh:
			df, ok := d.files[u.name]
			if !ok {
				fmt.Printf("Warning: %s is not being downloaded\n", u.name)
				continue
			}

			processUpdate(df, u)

			if u.err != nil {
				fmt.Printf("Download of %s failed: %v\n", u.name, u.err)
				delete(d.files, u.name)
			}

			if shuttingDown && !d.activeDownloads() {
				break DONE
			}
		}
	}

	wg.Wait()
}

func downloadFile(ctx context.Context, transport *http.Transport, URL string, progress progressCB) (string, error) {
	fmt.Printf("Downloading %s\n", URL)
	progressCh := make(chan downloadUpdate)
	downloadCh <- downloadRequest{
		progress:  progressCh,
		URL:       URL,
		ctx:       ctx,
		transport: transport,
	}
	fmt.Printf("request sent %s\n", URL)
	var d downloadUpdate
	for d = range progressCh {
		if d.err == nil {
			progress(d.p)
		}
	}
	if d.err != nil {
		return "", d.err
	}
	return d.path, nil
}
