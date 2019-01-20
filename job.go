package downloadLib

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

type Job struct {
	GID       string
	Filename  string
	TotalSize int64
	SaveDir   string
	TmpDir    string

	completeSize int64
	failCtx      context.Context
	failFunc     context.CancelFunc
	requests     []*request
	notify       chan struct{} // len is len(requests)
	doneCtx      context.Context
	doneFunc     context.CancelFunc
	speed        int64 // speed per second
}

func (j *Job) Err() error {
	select {
	case <-j.failCtx.Done():
		return errors.New("job is failed")
	default:
		return nil
	}
}

func (j *Job) Wait() bool {
	select {
	case <-j.doneCtx.Done():
		return true
	case <-j.failCtx.Done():
		return false
	}
}

func (j *Job) IsDone() bool {
	select {
	case <-j.doneCtx.Done():
		return true
	default:
		return false
	}
}

func (j *Job) IsFailed() bool {
	select {
	case <-j.failCtx.Done():
		return true
	default:
		return false
	}
}

func (j *Job) checkDone() {
	for i := 0; i < len(j.requests); i++ {
		select {
		case <-j.failCtx.Done():
			return
		case <-j.notify:
		}
	}
	log.Println("all sub requests complete")

	// create save file
	saveFile, err := os.Create(filepath.Join(j.SaveDir, j.GIDFilename()))
	if err != nil {
		j.failFunc()
		return
	}
	log.Println(saveFile.Stat())

	// write all tmp file content to save file
	for i := range j.requests {
		tmpFile, err := os.Open(filepath.Join(j.TmpDir, strconv.Itoa(i)))
		if err != nil {
			j.failFunc()
			return
		}
		if _, err := io.Copy(saveFile, tmpFile); err != nil {
			j.failFunc()
			saveFile.Close()
			tmpFile.Close()
			return
		}
		tmpFile.Close()
	}
	saveFile.Close()

	if err := os.RemoveAll(j.TmpDir); err != nil {
		log.Println("removing tmp dir error", err)
	}

	j.doneFunc()
}

func (j *Job) calculateSpeed() {
	var lastCompleteSize int64
	for j.CompleteSize() < j.TotalSize {
		completeSize := j.CompleteSize()
		atomic.StoreInt64(&j.speed, completeSize-lastCompleteSize)
		lastCompleteSize = completeSize
		time.Sleep(time.Second)
	}
}

func StartDownloadJob(url string, parallel int, savePath string) (j *Job, err error) {
	j = new(Job)

	resp, err := http.DefaultClient.Head(url)
	if err != nil {
		return nil, err
	}

	// get file size
	sizeStr := resp.Header.Get("Content-Length")
	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return nil, err
	}
	j.TotalSize = size

	// get file name
	var filename string
	if contentDisposition := resp.Header.Get("Content-Disposition"); contentDisposition != "" {
		if _, params, err := mime.ParseMediaType(contentDisposition); err == nil {
			filename = params["filename"]
		}
	}
	if filename == "" {
		filename = filepath.Base(resp.Request.URL.Path)
	}
	j.Filename = filename

	// check if target support parallel download
	if resp.Header.Get("Accept-Ranges") != "bytes" {
		// try to check again
		headReq, err := http.NewRequest(http.MethodHead, url, nil)
		if err != nil {
			return nil, err
		}
		headReq.Header.Set("Range", "bytes=0-1")
		resp, err := http.DefaultClient.Do(headReq)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusPartialContent {
			parallel = 1
		}
		resp.Body.Close()
	}

	resp.Body.Close()

	gid := genGid()

	j.GID = gid

	j.SaveDir = savePath

	// tmp dir is savePath/filename-gid-tmp
	tmpDir := filepath.Join(savePath, filename+"-"+gid+"-"+"tmp")
	j.TmpDir = tmpDir
	if err := os.Mkdir(tmpDir, os.FileMode(0770)); err != nil {
		return nil, err
	}

	j.doneCtx, j.doneFunc = context.WithCancel(context.Background())
	j.failCtx, j.failFunc = context.WithCancel(context.Background())

	partSize := size / int64(parallel)

	var pointer int64 = 0
	for i := 0; i < parallel; i++ {
		from := int64(i) * (pointer + partSize)
		to := from + partSize - 1
		if i == parallel-1 {
			to = size
		}

		req, err := newRequest(j, url, tmpDir, gid, i, from, to, j.failCtx)
		if err != nil {
			return nil, err
		}

		// try 3 times to start the downloading
		for i := 0; i < 3; i++ {
			if err = req.Do(); err == nil {
				break
			}
		}
		// start failed
		if err != nil {
			j.failFunc()
			return nil, err
		}

		j.requests = append(j.requests, req)
	}
	j.notify = make(chan struct{}, len(j.requests))

	go j.checkDone()
	go j.calculateSpeed()

	return j, nil
}

func (j *Job) CompleteSize() int64 {
	return atomic.LoadInt64(&j.completeSize)
}

func (j *Job) Speed() int64 {
	return atomic.LoadInt64(&j.speed)
}

func (j *Job) GIDFilename() string {
	return fmt.Sprintf("%s-%s", j.Filename, j.GID)
}

func (j *Job) FailChan() <-chan struct{} {
	return j.failCtx.Done()
}

func (j *Job) DoneChan() <-chan struct{} {
	return j.doneCtx.Done()
}
