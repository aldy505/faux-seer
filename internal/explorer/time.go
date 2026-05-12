package explorer

import (
	"time"
)

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
