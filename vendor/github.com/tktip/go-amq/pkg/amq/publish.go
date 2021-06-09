package amq

import (
	"github.com/Azure/go-amqp"
	"context"
	"fmt"
	"github.com/sirupsen/logrus"
	"time"
)

var (
	ErrorPublishBadTimeoutValue = fmt.Errorf("bad timeout value, must be above 0")
	ErrorPublishRetriesExceeded = fmt.Errorf("maximum retries exceeded")
)

func (s *Server) PublishMessage(queue string, timeout time.Duration, msg *amqp.Message) (err error) {
	if timeout <= 0 {
		return ErrorPublishBadTimeoutValue
	}

	s.incrementWaitGroup()
	defer s.decrementWaitGroup()

	ctx, cancel := context.WithTimeout(s.context, timeout)
	defer cancel()

	sleepBetweenAttempts := time.Duration(timeout.Nanoseconds()/int64(s.config.MaxSubRetries)) - time.Millisecond*100

	var sender *amqp.Sender
	for retries := 0; retries < s.config.MaxSubRetries; retries++ {

		//check err on 'continue'
		if err != nil {
			if contextError(err) {
				return err
			}
			time.Sleep(sleepBetweenAttempts)
		}

		sender, err = s.getSender(queue,
			amqp.LinkTargetAddress(queue),
		)

		if msg.Header == nil {
			msg.Header = &amqp.MessageHeader{Durable: true}
		}

		if msg.Properties == nil {
			msg.Properties = &amqp.MessageProperties{
				CreationTime: time.Now(),
				//Always want persistent
			}
		}

		if err != nil {
			s.logOnVerbose(logrus.ErrorLevel, "failed to create sender, error: '%s'", err.Error())
			continue
		}

		err = sender.Send(ctx, msg)
		if err != nil {
			s.logOnVerbose(logrus.ErrorLevel, "failed to send message, error: '%s'", err.Error())
			time.Sleep(sleepBetweenAttempts)
			continue
		}

		s.logOnVerbose(logrus.DebugLevel, "Successfully sent message '%s', headers '%v' [queue='%s']", msg.Value, msg.ApplicationProperties, queue)
		return nil
	}

	//Failed x times with sender, must be bad.
	s.logOnVerbose(logrus.WarnLevel, "failed to send message, maximum retries exceeded")
	return ErrorPublishRetriesExceeded
}

func (s *Server) PublishSimple(queue string, timeout time.Duration, body []byte, properties map[string]interface{}) (err error) {
	msg := amqp.NewMessage(body)
	msg.Value = body
	msg.Header = &amqp.MessageHeader{Durable: true}
	msg.ApplicationProperties = properties
	return s.PublishMessage(queue, timeout, msg)
}
