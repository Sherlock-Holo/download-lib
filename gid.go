package downloader

import (
	"encoding/hex"
	"math/rand"
	"sync"
)

var (
	rd *rand.Rand

	bufPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 16)
		},
	}
)

func genGid() string {
	buf := bufPool.Get().([]byte)
	defer bufPool.Put(buf)
	rd.Read(buf)
	gid := hex.EncodeToString(buf)
	return gid
}
