package downloadLib

import (
	"context"
	"encoding/binary"
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

	"github.com/etcd-io/bbolt"
	"golang.org/x/xerrors"
)

const (
	fileMode = 0644
)

type Job struct {
	GID       string
	Filename  string
	TotalSize int64
	SavePath  string

	completeSize   int64
	failCtx        context.Context    // if failCtx is done, job is failed
	failFunc       context.CancelFunc // fail the job
	requests       []*request
	doneNotify     chan struct{}      // len is len(requests)
	doneCtx        context.Context    // if doneCtx is done, job is done
	doneFunc       context.CancelFunc // finish the job
	speed          int64              // speed per second
	cancelCtx      context.Context    // if cancelCtx is done, job is canceled
	cancelFunc     context.CancelFunc // cancel the job
	deleteWait     *sync.WaitGroup    // wait for all sub-requests stop
	saveFile       *os.File
	err            error
	setErrOnce     sync.Once
	downloadDB     *bbolt.DB
	downloadDBPath string
	pauseCtx       context.Context    // if pauseCtx is done, job is paused
	pauseFunc      context.CancelFunc // pause job
}

func (j *Job) Wait() string {
	select {
	case <-j.doneCtx.Done():
		return "done"
	case <-j.failCtx.Done():
		return "failed"

	case <-j.cancelCtx.Done():

		select {
		case <-j.pauseCtx.Done():
			return "paused"
		default:
		}
		return "canceled"
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
	defer func() {
		if err := j.downloadDB.Close(); err != nil {
			log.Println(xerrors.Errorf("close download db file failed: %w", err))
		}

		// if just pause job, don't remove progress db file
		select {
		case <-j.pauseCtx.Done():
		default:
			// job is finished, cancel or failed
			if err := os.Remove(j.downloadDBPath); err != nil {
				log.Println(xerrors.Errorf("delete download db file failed: %w", err))
			}
		}
	}()

	for i := 0; i < len(j.requests); i++ {
		select {
		// jos is canceled or paused
		case <-j.cancelCtx.Done():
			// wait all sub requests stop
			j.deleteWait.Wait()
			j.saveFile.Close()

			// if just pause job, don't remove file
			select {
			case <-j.pauseCtx.Done():
			default:
				if err := os.Remove(filepath.Join(j.SavePath, j.GIDFilename())); err != nil {
					log.Printf("%+v", xerrors.Errorf("delete save file failed: %w", err))
				}
			}
			return

		case <-j.doneNotify: // wait sub request done
		}
	}

	j.saveFile.Close()

	j.doneFunc()
}

func (j *Job) calculateSpeed() {
	var lastCompleteSize int64
	for {
		select {
		case <-j.cancelCtx.Done():
			return
		case <-j.doneCtx.Done():
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
				return nil, xerrors.Errorf("mkdir download path failed: %w", err)
			}
		} else {
			return nil, xerrors.Errorf("check download path failed: %w", err)
		}
	}

	j = new(Job)

	resp, err := httpClient.Head(url)
	if err != nil {
		return nil, xerrors.Errorf("get url head response failed: %w", err)
	}
	defer resp.Body.Close()

	// get file size
	sizeStr := resp.Header.Get("content-length")
	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return nil, xerrors.Errorf("parse content length failed: %w", err)
	}
	j.TotalSize = size

	// get file name
	var filename string
	if contentDisposition := resp.Header.Get("content-disposition"); contentDisposition != "" {
		if _, params, err := mime.ParseMediaType(contentDisposition); err == nil {
			filename = params["filename"]
		}
	}
	if filename == "" {
		filename = filepath.Base(resp.Request.URL.Path)
	}
	j.Filename = filename

	// check if target support parallel download
	if resp.Header.Get("accept-ranges") != "bytes" {
		// try to check again
		headReq, err := http.NewRequest(http.MethodHead, url, nil)
		if err != nil {
			return nil, xerrors.Errorf("new http HEAD request failed: %w", err)
		}
		headReq.Header.Set("range", "bytes=0-1")
		resp, err := httpClient.Do(headReq)
		if err != nil {
			return nil, xerrors.Errorf("get url head response failed: %w", err)
		}
		if resp.StatusCode != http.StatusPartialContent {
			parallel = 1
		}
		resp.Body.Close()
	}

	j.GID = genGid()

	j.SavePath = savePath

	file, err := os.Create(filepath.Join(savePath, j.GIDFilename()))
	if err != nil {
		return nil, xerrors.Errorf("create download file failed: %w", err)
	}
	if err := file.Truncate(size); err != nil {
		return nil, xerrors.Errorf("truncate download file failed: %w", err)
	}

	j.saveFile = file

	j.doneCtx, j.doneFunc = context.WithCancel(context.Background())
	j.failCtx, j.failFunc = context.WithCancel(context.Background())
	j.cancelCtx, j.cancelFunc = context.WithCancel(context.Background())
	j.pauseCtx, j.pauseFunc = context.WithCancel(context.Background())

	j.deleteWait = &sync.WaitGroup{}

	// if size < 1KiB, disable parallel download because no need to do that
	if size < 1024*1024 {
		parallel = 1
	}

	partSize := size / int64(parallel)

	j.doneNotify = make(chan struct{}, parallel)

	// create progress db
	j.downloadDBPath = filepath.Join(savePath, fmt.Sprintf("%s.bbdb", j.GIDFilename()))
	j.downloadDB, err = bbolt.Open(j.downloadDBPath, fileMode, nil)
	if err != nil {
		return nil, xerrors.Errorf("create download db failed: %w", err)
	}

	// create head info bucket
	if err := j.downloadDB.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucket([]byte("head"))
		if err != nil {
			return xerrors.Errorf("create head bucket failed: %w", err)
		}

		if err := bucket.Put([]byte("gid"), []byte(j.GID)); err != nil {
			return xerrors.Errorf("save gid failed: %w", err)
		}

		if err := bucket.Put([]byte("filename"), []byte(j.Filename)); err != nil {
			return xerrors.Errorf("save filename failed: %w", err)
		}

		if err := bucket.Put([]byte("url"), []byte(url)); err != nil {
			return xerrors.Errorf("save url failed: %w", err)
		}

		totalSize := make([]byte, 8)
		binary.BigEndian.PutUint64(totalSize, uint64(j.TotalSize))
		if err := bucket.Put([]byte("total size"), totalSize); err != nil {
			return xerrors.Errorf("save total size failed: %w", err)
		}

		return nil
	}); err != nil {
		return nil, xerrors.Errorf("create head db info failed: %w", err)
	}

	if err := j.downloadDB.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucket([]byte("sub-requests"))
		if err != nil {
			return xerrors.Errorf("create sub-requests bucket failed: %w", err)
		}

		for i := 0; i < parallel; i++ {
			if err := bucket.Put([]byte{byte(i)}, []byte{byte(i)}); err != nil {
				return xerrors.Errorf("create request ID %d failed: %w", i, err)
			}
		}

		return nil

	}); err != nil {
		return nil, xerrors.Errorf("create sub-requests failed: %w", err)
	}

	var pointer int64 = 0
	for i := 0; i < parallel; i++ {
		from := int64(i) * (pointer + partSize)
		to := from + partSize - 1
		if i == parallel-1 {
			to = size
		}

		req := newRequest(j, uint8(i), url, from, to, j.saveFile, j.cancelCtx, j.deleteWait)

		// try 3 times to start the downloading
		for i := 0; i < 3; i++ {
			if err = req.Do(); err == nil {
				break
			}
		}
		// start failed
		if err != nil {
			j.failFunc()
			return nil, xerrors.Errorf("start sub requests failed: %w", err)
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

	j.cancelFunc()
	return "cancel"
}

func (j *Job) Err() error {
	return j.err
}

func (j *Job) Pause() string {
	j.pauseFunc()

	cancel := j.Cancel()
	switch cancel {
	case "done":
		return "done"

	case "cancel":
		return "paused"

	default:
		return cancel
	}
}
