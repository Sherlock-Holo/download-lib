package downloadLib

import (
	"context"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type Job struct {
	GID       string
	Filename  string
	TotalSize int64
	SavePath  string

	completeSize    int64
	failCtx         context.Context
	failFunc        context.CancelFunc
	requests        []*request
	doneNotify      chan struct{} // len is len(requests)
	doneCtx         context.Context
	doneFunc        context.CancelFunc
	speed           int64 // speed per second
	cancelCtx       context.Context
	cancelFunc      context.CancelFunc
	runningCtx      context.Context
	stopRunningFunc context.CancelFunc
	deleteWait      *sync.WaitGroup
	saveFile        *os.File
	err             error
	setErrOnce      sync.Once
}

func (j *Job) Wait() string {
	select {
	case <-j.doneCtx.Done():
		return "done"
	case <-j.failCtx.Done():
		return "failed"

	case <-j.cancelCtx.Done():
		return "cancel"
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

func (j *Job) IsCancel() bool {
	select {
	case <-j.cancelCtx.Done():
		return true
	default:
		return false
	}
}

func (j *Job) checkDone() {
	for i := 0; i < len(j.requests); i++ {
		select {
		// jos is not running
		case <-j.runningCtx.Done():
			// wait all sub requests stop
			j.deleteWait.Wait()
			j.saveFile.Close()

			if err := os.Remove(filepath.Join(j.SavePath, j.GIDFilename())); err != nil {
				log.Println("removing save file error", err)
			}
			return

		case <-j.doneNotify:
		}
	}

	j.saveFile.Close()

	j.doneFunc()
}

func (j *Job) calculateSpeed() {
	var lastCompleteSize int64
	for {
		select {
		case <-j.runningCtx.Done():
			return
		case <-j.doneCtx.Done():
			return
		case <-j.failCtx.Done():
			return
		case <-j.cancelCtx.Done():
			return

		default:
			completeSize := j.CompleteSize()
			atomic.StoreInt64(&j.speed, completeSize-lastCompleteSize)
			lastCompleteSize = completeSize
			time.Sleep(time.Second)
		}
	}
}

func StartDownloadJob(url string, parallel int, savePath string) (j *Job, err error) {
	if _, err := os.Stat(savePath); err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(savePath, os.ModePerm); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

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

	j.SavePath = savePath

	file, err := os.Create(filepath.Join(savePath, j.GIDFilename()))
	if err != nil {
		return nil, err
	}
	if err := file.Truncate(size); err != nil {
		return nil, err
	}

	j.saveFile = file

	j.doneCtx, j.doneFunc = context.WithCancel(context.Background())
	j.failCtx, j.failFunc = context.WithCancel(context.Background())
	j.cancelCtx, j.cancelFunc = context.WithCancel(context.Background())
	j.runningCtx, j.stopRunningFunc = context.WithCancel(context.Background())

	j.deleteWait = &sync.WaitGroup{}

	// if size < 1024*1024, disable parallel download because no need to do that
	if size < 1024*1024 {
		parallel = 1
	}

	partSize := size / int64(parallel)

	j.doneNotify = make(chan struct{}, parallel)

	var pointer int64 = 0
	for i := 0; i < parallel; i++ {
		from := int64(i) * (pointer + partSize)
		to := from + partSize - 1
		if i == parallel-1 {
			to = size
		}

		req, err := newRequest(j, url, from, to, j.saveFile, j.runningCtx, j.deleteWait)
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

func (j *Job) CancelChan() <-chan struct{} {
	return j.cancelCtx.Done()
}

func (j *Job) Cancel() string {
	select {
	case <-j.DoneChan():
		return "done"

	case <-j.FailChan():
		return "failed"

	default:
	}

	j.stopRunningFunc()
	j.cancelFunc()
	return "cancel"
}

func (j *Job) Err() error {
	return j.err
}
