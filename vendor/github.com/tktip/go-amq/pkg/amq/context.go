package amq

import "context"

func contextError(err error) bool {
	if err == nil {
		return false
	}

	switch err {
	case context.Canceled:
		return true
	case context.DeadlineExceeded:
		return true
	default:
		return false
	}
}
