package downloadLib

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"

	"github.com/etcd-io/bbolt"
	"golang.org/x/xerrors"
)

// if error is not nil, you may need to check job is nil or not,
// if job is non-nil, job.Err() may save an error
func RecoverJob(downloadDBPath string) (*Job, error) {
	db, err := bbolt.Open(downloadDBPath, fileMode, nil)
	if err != nil {
		return nil, xerrors.Errorf("open download db failed: %w", err)
	}

	job := new(Job)

	job.downloadDB = db
	job.downloadDBPath = downloadDBPath

	// recover save path
	job.SavePath = filepath.Dir(downloadDBPath)

	var url string
	if err := db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("head"))
		if bucket == nil {
			return xerrors.New("head info doesn't exist")
		}

		// recover gid
		if gid := bucket.Get([]byte("gid")); gid != nil {
			job.GID = string(gid)
		} else {
			return xerrors.New("gid doesn't exist")
		}

		// recover filename
		if filename := bucket.Get([]byte("filename")); filename != nil {
			job.Filename = string(filename)
		} else {
			return xerrors.New("filename doesn't exist")
		}

		job.saveFile, err = os.OpenFile(filepath.Join(job.SavePath, job.GIDFilename()), os.O_RDWR, fileMode)
		if err != nil {
			return xerrors.Errorf("open file failed: %w", err)
		}

		// recover url
		if urlBytes := bucket.Get([]byte("url")); urlBytes != nil {
			url = string(urlBytes)
		} else {
			return xerrors.New("url doesn't exist: %w")
		}

		// recover total size
		totalSize := bucket.Get([]byte("total size"))
		if totalSize == nil {
			return xerrors.New("total size doesn't exist")
		}
		job.TotalSize = int64(binary.BigEndian.Uint64(totalSize))

		bucket = tx.Bucket([]byte("sub-requests"))
		if bucket == nil {
			return xerrors.New("sub-requests bucket doesn't exist")
		}

		job.doneCtx, job.doneFunc = context.WithCancel(context.Background())
		job.failCtx, job.failFunc = context.WithCancel(context.Background())
		job.cancelCtx, job.cancelFunc = context.WithCancel(context.Background())
		job.pauseCtx, job.pauseFunc = context.WithCancel(context.Background())

		job.deleteWait = &sync.WaitGroup{}

		var subRequestIDs []uint8
		_ = bucket.ForEach(func(k, v []byte) error {
			subRequestIDs = append(subRequestIDs, k[0])
			return nil
		})

		for _, id := range subRequestIDs {
			bucket = tx.Bucket([]byte{id})
			if bucket == nil {
				return xerrors.New("sub-request ID doesn't exist")
			}

			switch string(bucket.Get([]byte("status"))) {
			case "done":
				continue

			default:
				return xerrors.New("sub-request status error")

			case "running":
			}

			fromBytes := bucket.Get([]byte("from"))
			if len(fromBytes) != 8 {
				return xerrors.New("sub-request data from doesn't exist or is illegal")
			}
			from := binary.BigEndian.Uint64(fromBytes)

			toBytes := bucket.Get([]byte("to"))
			if len(toBytes) != 8 {
				return xerrors.New("sub-request data to doesn't exist or is illegal")
			}
			to := binary.BigEndian.Uint64(toBytes)

			subRequest := newRequest(job, id, url, int64(from), int64(to), job.saveFile, job.cancelCtx, job.deleteWait)
			if err != nil {
				return xerrors.Errorf("create sub-request failed: %w", err)
			}
			job.requests = append(job.requests, subRequest)
		}

		return nil

	}); err != nil {
		return nil, xerrors.Errorf("read download failed: %w", err)
	}

	job.doneNotify = make(chan struct{}, len(job.requests))

	// start all sub-requests
	for _, req := range job.requests {
		for i := 0; i < 3; i++ {
			if err = req.Do(); err == nil {
				break
			}
		}

		// start failed
		if err != nil {
			job.failFunc()
			return job, xerrors.Errorf("start sub-request failed: %w", err)
		}
	}

	go job.checkDone()
	go job.calculateSpeed()

	return job, nil
}
