package downloader

import (
	"math/rand"
	"time"
)

func init() {
	rd = rand.New(rand.NewSource(time.Now().UnixNano()))
}
