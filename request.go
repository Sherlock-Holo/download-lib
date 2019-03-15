package downloadLib

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"

	"github.com/etcd-io/bbolt"
	"golang.org/x/xerrors"
)

const bufSize = 128 * 1024

type request struct {
	Job  *Job
	Url  string
	From int64
	To   int64
	Ctx  context.Context
	// Cancel     context.CancelFunc
	retry      int
	DeleteWait *sync.WaitGroup
	saveFile   *os.File
	ID         uint8
}

func newRequest(job *Job, id uint8, url string, from, to int64, saveFile *os.File, parentCtx context.Context, deleteWait *sync.WaitGroup) *request {
	req := &request{
		Job:        job,
		Url:        url,
		From:       from,
		To:         to,
		saveFile:   saveFile,
		DeleteWait: deleteWait,
		ID:         id,
	}

	req.Ctx, _ = context.WithCancel(parentCtx)

	return req
}

func (req *request) Do() error {
	req.DeleteWait.Add(1)
	return req.do(req.From)
}

func (req *request) do(from int64) error {
	httpReq, err := http.NewRequest(http.MethodGet, req.Url, nil)
	if err != nil {
		return xerrors.Errorf("create sub-get-request error: %W", err)
	}
	httpReq = httpReq.WithContext(req.Ctx)

	httpReq.Header.Set("range", fmt.Sprintf("bytes=%d-%d", from, req.To))

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return xerrors.Errorf("start sub-request error: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
	default:
		resp.Body.Close()
		req.Job.cancelFunc()
		req.Job.failFunc()
		return xerrors.New("sub request is not OK, status is " + resp.Status)
	}

	if err := req.Job.downloadDB.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte{req.ID})
		if err != nil {
			return xerrors.Errorf("create sub request bucket failed: %w", err)
		}

		if err := bucket.Put([]byte("status"), []byte("running")); err != nil {
			return xerrors.Errorf("create status failed: %w", err)
		}

		to := make([]byte, 8)
		binary.BigEndian.PutUint64(to, uint64(req.To))
		if err := bucket.Put([]byte("to"), to); err != nil {
			return xerrors.Errorf("save data to failed: %w", err)
		}

		return nil

	}); err != nil {
		resp.Body.Close()
		req.Job.cancelFunc()
		req.Job.failFunc()
		return xerrors.Errorf("create sub request db info failed: %w", err)
	}

	go download(req, resp)

	return nil
}

func download(req *request, resp *http.Response) {
	buf := make([]byte, bufSize)
	for {
		n, err := resp.Body.Read(buf)

		// first write receive data if no error
		if err == nil || err == io.EOF {
			if _, err := req.saveFile.WriteAt(buf[:n], req.From); err != nil {
				// set job err
				req.Job.setErrOnce.Do(func() {
					req.Job.err = xerrors.Errorf("write data failed: %w", err)
				})

				// buf write error, it may can't recover
				resp.Body.Close()
				req.Job.cancelFunc()
				req.Job.failFunc()
				req.DeleteWait.Done()
				return
			}

			req.From += int64(n)
			// update job completeSize
			atomic.AddInt64(&req.Job.completeSize, int64(n))

			if err := req.Job.downloadDB.Update(func(tx *bbolt.Tx) error {
				bucket := tx.Bucket([]byte{req.ID})
				if bucket == nil {
					return xerrors.Errorf("sub request bucket miss: %w", err)
				}

				from := make([]byte, 8)
				binary.BigEndian.PutUint64(from, uint64(req.From))
				if err := bucket.Put([]byte("from"), from); err != nil {
					return xerrors.Errorf("save data from failed: %w", err)
				}

				return nil

			}); err != nil {
				// progress db update error
				// set job err
				req.Job.setErrOnce.Do(func() {
					req.Job.err = xerrors.Errorf("update job complete size failed: %w", err)
				})

				resp.Body.Close()
				req.Job.cancelFunc()
				req.Job.failFunc()
				req.DeleteWait.Done()
				return
			}
		}

		// all data has been read
		if err == io.EOF {
			resp.Body.Close()

			if err := req.Job.downloadDB.Update(func(tx *bbolt.Tx) error {
				bucket := tx.Bucket([]byte{req.ID})
				if bucket == nil {
					return xerrors.Errorf("sub reuqest bucket miss: %w", err)
				}

				if err := bucket.Put([]byte("status"), []byte("done")); err != nil {
					return xerrors.Errorf("save sub request status failed: %w", err)
				}

				return nil

			}); err != nil {
				// progress db update error
				// set job err
				req.Job.setErrOnce.Do(func() {
					req.Job.err = xerrors.Errorf("save sub request status failed: %w", err)
				})

				resp.Body.Close()
				req.Job.cancelFunc()
				req.Job.failFunc()
				req.DeleteWait.Done()
				return
			}

			// notify job this sub request is done
			req.Job.doneNotify <- struct{}{}

			// let deleteWait done too, because maybe a sub request done but others don't,
			// if job cancel, we need to make deleteWait done too
			req.DeleteWait.Done()
			return
		}

		// if some error happened
		if err != nil {
			// check if job
			select {
			case <-req.Ctx.Done():
				// job is canceled or paused
				resp.Body.Close()
				req.DeleteWait.Done()
				return

			default:
			}

			resp.Body.Close()

			// retry
			for req.retry < 3 {
				req.retry++
				if err = req.do(req.From); err == nil {
					return
				} else if xerrors.Is(err, context.Canceled) {
					// job is canceled or paused
					req.DeleteWait.Done()
					return
				}
			}

			// retry more than 3 times, job fail
			// set job err
			req.Job.setErrOnce.Do(func() {
				req.Job.err = xerrors.Errorf("sub-request downloads error: %w", err)
			})

			req.Job.cancelFunc()
			req.Job.failFunc()
			req.DeleteWait.Done()
			return
		}

		select {
		// check job is canceled or paused
		case <-req.Ctx.Done():
			resp.Body.Close()
			req.DeleteWait.Done()
			return

		default:
		}
	}
}
