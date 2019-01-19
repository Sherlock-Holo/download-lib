package downloader

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
)

const bufSize = 128 * 1024

type request struct {
	Gid     string
	Job     *Job
	Id      int
	Url     string
	From    int64
	To      int64
	TmpFile *os.File
	Ctx     context.Context
	Cancel  context.CancelFunc
	retry   int
}

func newRequest(job *Job, url, tmpDir, gid string, id int, from, to int64, parentCtx context.Context) (*request, error) {
	req := new(request)

	req.Gid = gid
	req.Job = job
	req.Id = id
	req.Url = url
	req.From = from
	req.To = to

	tmpFile, err := os.Create(filepath.Join(tmpDir, strconv.Itoa(id)))
	if err != nil {
		return nil, err
	}
	req.TmpFile = tmpFile

	req.Ctx, req.Cancel = context.WithCancel(parentCtx)

	return req, nil
}

func (req *request) Do() error {
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
		req.Job.failFunc()
		return fmt.Errorf("http status error %s", resp.Status)
	}

	writer := bufio.NewWriter(req.TmpFile)

	log.Println("start download", req.Url, req.From, req.To)
	go func() {
		buf := make([]byte, bufSize)
		for {
			n, err := resp.Body.Read(buf)

			// first write receive data if no error
			if err == nil || err == io.EOF {
				writer.Write(buf[:n])
				req.From += int64(n)
				// update job completeSize
				atomic.AddInt64(&req.Job.completeSize, int64(n))
			}

			// read all data
			if err == io.EOF {
				writer.Flush()
				req.TmpFile.Close()
				resp.Body.Close()

				log.Println("sub request complete")

				// notify job this sub request is done
				req.Job.notify <- struct{}{}
				return
			}

			// if some error happened
			if err != nil {
				// check if job is failed
				select {
				case <-req.Ctx.Done():
					resp.Body.Close()
					writer.Flush()
					req.TmpFile.Close()
					return
				default:
				}

				resp.Body.Close()
				writer.Flush()
				// retry
				for req.retry < 3 {
					req.retry++
					if req.do(req.From) == nil {
						return
					}
				}

				// retry more than 3 times, job fail
				req.Job.failFunc()
				req.TmpFile.Close()
			}
		}
	}()

	return nil
}
