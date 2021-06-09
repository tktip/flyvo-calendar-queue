package main

import (
	"github.com/haraldfw/cfger"
	"github.com/sirupsen/logrus"
	"github.com/tktip/flyvo-calendar-queue/internal/health"
	"github.com/tktip/flyvo-calendar-queue/internal/queue"
)

type generalConfig struct {
	Debug bool `yaml:"debug"`
}

func main() {

	conf := generalConfig{}
	_, err := cfger.ReadStructuredCfg("env::CONFIG", &conf)
	if err != nil {
		logrus.Fatalf("Couldn't read config: %s", err.Error())
	}

	if conf.Debug == true {
		logrus.SetLevel(logrus.DebugLevel)
		logrus.Debug("Debug mode enabled")
	}

	handler := queue.Handler{}
	_, err = cfger.ReadStructuredCfg("env::CONFIG", &handler)
	if err != nil {
		logrus.Fatalf("Couldn't read config: %s", err.Error())
	}

	if handler.ExponentBase <= 1 {
		logrus.Fatalf("An exponent below 1 makes no sense!")
	}

	go health.StartHandlerIfEnabled()

	logrus.Infof("Config: %+v", handler)
	logrus.Fatal(handler.Run())
}
