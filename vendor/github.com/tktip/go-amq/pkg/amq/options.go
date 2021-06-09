package amq

import (
	"crypto/tls"
	"github.com/sirupsen/logrus"
	"time"
)

type ServerOption func(s *Server)

func ServerOptionConnectTimeout(timeout time.Duration) ServerOption {
	return func(s *Server) {
		s.logOnVerbose(logrus.InfoLevel, "Server max timeout set to %v", timeout)
		s.config.ConnTimeout = timeout
	}
}
func ServerOptionMaxReadRetries(retries int) ServerOption {
	return func(s *Server) {
		s.logOnVerbose(logrus.InfoLevel, "Server max retry count set to %v", retries)
		s.config.MaxSubRetries = retries
	}
}
func ServerOptionMaxWriteRetries(retries int) ServerOption {
	return func(s *Server) {
		s.logOnVerbose(logrus.InfoLevel, "Server max retry count set to %v", retries)
		s.config.MaxPubRetries = retries
	}
}

func ServerOptionVerbose(verbose bool) ServerOption {
	return func(s *Server) {
		s.logOnVerbose(logrus.InfoLevel, "Verbose logging set to '%v'", verbose)
		s.config.Verbose = verbose
	}
}

func ServerOptionSleepTime(timeBetweenAttempts time.Duration) ServerOption {
	return func(s *Server) {
		s.logOnVerbose(logrus.InfoLevel, "Server SleepTime time set to %v", timeBetweenAttempts)
		s.config.SleepTime = timeBetweenAttempts
	}
}

func ServerOptionTLS(t *tls.Config) ServerOption {
	return func(s *Server) {
		s.logOnVerbose(logrus.InfoLevel, "Server tls enabled")
		s.config.TLS = t
	}
}
