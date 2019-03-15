package downloadLib

import (
	"net/http"
	"time"
)

var httpClient = http.Client{
	Timeout: 10 * time.Second,
}
