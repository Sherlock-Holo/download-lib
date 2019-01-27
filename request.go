package downloadLib

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"

	"golang.org/x/exp/xerrors"
)

const bufSize = 128 * 1024

type request struct {
	Job        *Job
	Url        string
	From       int64
	To         int64
	Ctx        context.Context
	Cancel     context.CancelFunc
	retry      int
	DeleteWait *sync.WaitGroup
	saveFile   *os.File
}

func newRequest(job *Job, url string, from, to int64, saveFile *os.File, parentCtx context.Context, deleteWait *sync.WaitGroup) (*request, error) {
	req := new(request)

	req.Job = job
	req.Url = url
	req.From = from
	req.To = to

	req.saveFile = saveFile

	req.Ctx, req.Cancel = context.WithCancel(parentCtx)

	req.DeleteWait = deleteWait

	return req, nil
}

func (req *request) Do() error {
	req.DeleteWait.Add(1)
	return req.do(req.From)
}

func (req *request) do(from int64) error {
	httpReq, err := http.NewRequest(http.MethodGet, req.Url, nil)
	if err != nil {
		return err
	}
	httpReq = httpReq.WithContext(req.Ctx)

	httpReq.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, req.To))

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return err
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
	default:
		resp.Body.Close()
		req.Job.stopRunningFunc()
		req.Job.failFunc()
		return fmt.Errorf("http status error %s", resp.Status)
	}

	// log.Println("start download", req.Url, req.From, req.To)
	go func() {
		buf := make([]byte, bufSize)
		for {
			n, err := resp.Body.Read(buf)

			// first write receive data if no error
			if err == nil || err == io.EOF {
				if _, err := req.saveFile.WriteAt(buf[:n], req.From); err != nil {
					// set job err
					req.Job.setErrOnce.Do(func() {
						req.Job.err = xerrors.Errorf("%s: %w", err, ErrBase)
					})
					// buf write error, it may can't recover
					resp.Body.Close()
					req.Job.stopRunningFunc()
					req.Job.failFunc()
					req.DeleteWait.Done()
					return
				}

				req.From += int64(n)
				// update job completeSize
				atomic.AddInt64(&req.Job.completeSize, int64(n))
			}

			// read all data
			if err == io.EOF {
				resp.Body.Close()
				// notify job this sub request is done
				req.Job.doneNotify <- struct{}{}
				return
			}

			// if some error happened
			if err != nil {
				// set job err
				req.Job.setErrOnce.Do(func() {
					req.Job.err = err
				})

				// check if job is stop
				select {
				case <-req.Ctx.Done():
					// job is stop
					resp.Body.Close()
					req.DeleteWait.Done()
					return

				default:
				}

				resp.Body.Close()

				// retry
				for req.retry < 3 {
					req.retry++
					if req.do(req.From) == nil {
						return
					}
				}

				// retry more than 3 times, job fail
				// set job err
				req.Job.setErrOnce.Do(func() {
					req.Job.err = err
				})
				req.Job.stopRunningFunc()
				req.Job.failFunc()
				req.DeleteWait.Done()
				return
			}
		}
	}()

	return nil
}
