package queue

import (
	"context"
	"encoding/json"
	"errors"
	"io/ioutil"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/tktip/flyvo-calendar-queue/pkg/model"
	"github.com/tktip/go-amq/pkg/amq"

	//model "github.com/tktip/google-calendar/pkg/googlecal"
	"github.com/Azure/go-amqp"
)

var (
	client = &http.Client{
		Timeout: time.Second * 30,
	}
	ctx = context.Background()
)

//Handler - handles enqueuing/dequeueing and pushing to gcal
type Handler struct {
	CalendarRootURL string   `yaml:"calendarCreateEventUrl"`
	QueueName       string   `yaml:"queueName"`
	ErrorQueueName  string   `yaml:"errorQueueName"`
	ActiveMQBrokers []string `yaml:"activemqBrokers"`

	DontUseBackoff      bool          `yaml:"dontUseBackoff"`
	BaseSleepTime       time.Duration `yaml:"baseWaitTime"`
	BaseBackoffTime     time.Duration `yaml:"baseBackoffTime"`
	MaxBackoff          time.Duration `yaml:"maxBackoff"`
	MaxSleepTime        time.Duration `yaml:"maxSleepTime"`
	ExponentBase        float64       `yaml:"exponentBase"`
	FailureExponentBase float64       `yaml:"backoffBase"`

	srv                 *amq.Server
	backoffCounter      int
	consecutiveFailures int
}

//Accepts any and all requests and pushes the message with body and query params
//onto the activemq queue.
func (h *Handler) handleHTTPRequest(c *gin.Context) {
	data, err := c.GetRawData()
	if err != nil {
		logrus.Errorf("Failed to get data: %s", err.Error())
		c.String(http.StatusInternalServerError, "Failed to read data: %s", data)
		return
	}

	params := map[string]string{}
	for k := range c.Request.URL.Query() {
		params[k] = c.Query(k)
	}

	body := model.CalendarRequest{
		Path:   c.Param("path"),
		Method: c.Request.Method,
		Body:   string(data),
		Params: params,
	}

	data, err = json.Marshal(&body)
	if err != nil {
		logrus.Errorf("Failed to marshal data: %s", err.Error())
		c.String(http.StatusInternalServerError, "Failed to marshal data: %s", data)
		return
	}

	logrus.Debugf("Received data:\n'%s'", data)
	err = h.srv.PublishSimple(h.QueueName, time.Second*10, data, map[string]interface{}{
		"persistent": "true",
	})
	if err != nil {
		logrus.Errorf("Failed to post to queue: %s", err.Error())
		c.String(http.StatusInternalServerError, "Failed to post to queue: %s", data)
		return
	}

	c.Status(http.StatusOK)
}

func (h *Handler) validate() error {
	if h.ErrorQueueName == "" {
		return errors.New("missing error queue")
	}

	if h.QueueName == "" {
		return errors.New("missing source queue")
	}

	if h.ExponentBase <= 1 {
		return errors.New("exponent base must be > 1")
	}

	if h.CalendarRootURL == "" {
		return errors.New("missing calendar url")
	}

	if len(h.ActiveMQBrokers) == 0 {
		return errors.New("no activemq brokers provided")
	}

	if h.FailureExponentBase <= 1 {
		return errors.New("failure exponent base must be > 1")
	}
	return nil
}

//Run - run the handler
func (h *Handler) Run() error {
	if err := h.validate(); err != nil {
		return err
	}

	logrus.Debug("Starting activemq")

	//start activemq connection
	srv, err := amq.NewServer(ctx, h.ActiveMQBrokers)
	if err != nil {
		return err
	}
	h.srv = srv

	logrus.Debugf("Subscribing to queue '%s'", h.QueueName)
	_, err = h.srv.Subscribe(h.QueueName, h.sendMsgToCalendar)
	if err != nil {
		return err
	}

	logrus.Info("Successfully connected to amq, starting Gin server now.")

	//set up simple gin server
	g := gin.New()
	g.Any("/*path", h.handleHTTPRequest)
	return g.Run()
}

func (h *Handler) doCalRequest(body model.CalendarRequest) (
	respData string,
	respStatus int,
	err error,
) {
	logrus.Debugf("Performing calendar request: %v", body)
	var req *http.Request
	req, err = http.NewRequest(
		body.Method,
		h.CalendarRootURL+body.Path,
		strings.NewReader(body.Body),
	)

	if err != nil {
		return
	}

	v := url.Values{}
	for key, val := range body.Params {
		v.Set(key, val)
	}

	req.URL.RawQuery = v.Encode()

	var resp *http.Response
	resp, err = client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	respStatus = resp.StatusCode
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		respData = "[unable to return response data:" + err.Error() + "]"
		err = nil
		return
	}
	respData = string(data)

	return
}

func (h *Handler) sendMsgToCalendar(
	_ string,
	message *amqp.Message,
	err amq.ReceiverError,
) {
	msgID := message.Properties.MessageID

	if err != nil {
		logrus.Errorf("%s: Failed getting message from amq: %s",
			msgID,
			err.Error(),
		)
		message.Release(ctx)
		logrus.Debugf("%s: Released", msgID)
		return
	}

	//grab and marshal data from queue
	msgData := message.GetData()
	logrus.Debugf("%s: Got activemq data: %s", msgID, msgData)

	body := model.CalendarRequest{}
	err = json.Unmarshal(msgData, &body)
	if err != nil {
		logrus.Errorf("%s: Could not unmarshal (%s): %s",
			msgID,
			err.Error(),
			msgData,
		)

		message.Reject(ctx, nil)
		logrus.Debugf("%s: Rejected", msgID)
		return
	}

	logrus.Infof("%s: Sending request to calendar integration: %s",
		msgID,
		msgData,
	)

	now := time.Now()

	//send request to tip google calendar api
	respBody, respStatus, err := h.doCalRequest(body)
	if err != nil {
		message.Release(ctx)
		logrus.Debugf("%s: Released", msgID)
		logrus.Errorf("%s: Failed to perform request to google calendar: %s",
			msgID,
			err.Error(),
		)

		//Likely calendar not up
		logrus.Info("Sleeping for 60s to wait for google-calendar")
		time.Sleep(time.Second * 60)
		return
	}

	//set default sleep time
	sleepTime := h.getSleepTime()
	logrus.Debugf("CURRENT SLEEP TIME: %s", sleepTime)

	//on success, all is good.
	if respStatus < 300 && respStatus >= 200 {
		logrus.Infof("%s: Success. Response from gcal (%d): '%s'",
			msgID,
			respStatus,
			respBody,
		)

		message.Accept(ctx)
		logrus.Debugf("%s: Accepted", msgID)

		//On success, reset backoff and assume we're good at current speed.
		//h.backoffCounter = 0
		goto sleep
	}

	logrus.Errorf("%s: Failure. Unable to create event in google (%d): '%s'",
		msgID,
		respStatus,
		respBody,
	)

	//Rate limit exceeded means we're pushing too frequently.
	//We should wait for a longer time.
	if strings.Contains(strings.ToLower(respBody), "ratelimitexceeded") {
		message.Release(ctx)
		logrus.Debugf("%s: Released", msgID)

		sleepTime = h.getBackoffWait()
		h.consecutiveFailures += 1
		h.backoffCounter += 1
		logrus.Warnf("%s: Rate limit exceeded - %d failures in a row. Sleeping for '%s'",
			msgID,
			h.consecutiveFailures,
			sleepTime,
		)
		//goto sleep
	} else {
		//Googles use of respStatus seems arbitrary?
		//Or is it my google calendar code? Probably the latter..
		message.Accept(ctx)
		logrus.Debugf("%s: Accepted", msgID)

		logrus.Debugf("%s: Sending msg to %s", msgID, h.ErrorQueueName)
		err = h.srv.PublishSimple(h.ErrorQueueName, time.Second*15, msgData, nil)
		if err != nil {
			logrus.Errorf("%s: Failed to push msg to backupQueue: %s",
				msgID,
				err.Error(),
			)
		}

	}

sleep:
	sleepTime = sleepTime - time.Now().Sub(now)
	if sleepTime > 0 {
		logrus.Debugf("%s: Sleeping for %s", msgID, sleepTime)
		time.Sleep(sleepTime)
	} else {
		logrus.Debugf("%s: Took longer than sleep time - moving on.", msgID)
	}
}

func (h *Handler) getBackoffWait() time.Duration {
	//2 min
	//4 min
	//6 min
	//10 min
	//18 min
	//34 min
	if h.backoffCounter == 0 {
		return h.BaseBackoffTime
	}

	waitTime := h.BaseBackoffTime + time.Minute*30*time.Duration(
		math.Pow(
			float64(h.FailureExponentBase),
			float64(h.backoffCounter),
		),
	)

	if waitTime > h.MaxBackoff {
		return h.MaxBackoff
	}

	logrus.Warnf("BACKING OFF: SLEEPING FOR %s", waitTime)
	return waitTime
}

func (h *Handler) getSleepTime() time.Duration {
	if h.DontUseBackoff {
		return h.BaseSleepTime
	}

	waitTime := h.BaseSleepTime + time.Millisecond*100*time.Duration(
		math.Pow(
			float64(h.ExponentBase),
			float64(h.consecutiveFailures),
		),
	)

	if waitTime > h.MaxSleepTime {
		return h.MaxSleepTime
	}

	return waitTime
}
