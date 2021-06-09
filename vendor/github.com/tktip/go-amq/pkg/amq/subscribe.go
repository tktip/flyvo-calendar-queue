package amq

import (
	"context"
	"fmt"
	"github.com/Azure/go-amqp"
	"github.com/sirupsen/logrus"
	"io"
	"strings"
	"time"
)

type ReceiverError error

var (
	ErrorSubscriptionRetriesExceeded = fmt.Errorf("maximum retries exceeded")
	ErrorSubscriptionDoesntExist     = fmt.Errorf("no subscription mapped to provided id")
)

type subscription struct {
	queue      string
	context    context.Context
	cancelFunc context.CancelFunc
	callback   ListenCallback
}

type ListenCallback func(queue string, message *amqp.Message, err ReceiverError)

func releaseOnPanic(msg *amqp.Message) {
	if p := recover(); p != nil {
		msg.Release(context.Background())
		panic(p)
	}
}

func (s *Server) Subscribe(queue string, callback ListenCallback) (subscriptionID int, err error) {

	ctx, cancelFunc := context.WithCancel(s.context)
	subscriptionID = s.trackSubscription(&subscription{
		cancelFunc: cancelFunc,
		queue:      queue,
		context:    ctx,
		callback:   callback,
	})

	go func() {
		defer cancelFunc()
		defer s.Unsubscribe(subscriptionID)

		for {
			attempts := 0
			receiver, err := s.getReceiver(
				amqp.LinkSourceAddress(queue),
			)
			if err != nil {
				if contextError(err) {
					return
				}
				time.Sleep(s.config.SleepTime)
				continue
			}

			for attempts < s.config.MaxSubRetries {
				msg, err := receiver.Receive(ctx)
				if err != nil && strings.Contains(err.Error(), "read tcp") || err == io.EOF {
					s.logOnVerbose(logrus.WarnLevel, "Queue %s - Lost connection to Server: %s", queue, err.Error())
					time.Sleep(s.config.SleepTime)
					break
				} else if contextError(err) {
					s.logOnVerbose(logrus.InfoLevel, "Queue %s - Canceled - ending subscription [%v]", queue, subscriptionID)
					callback(queue, nil, err)
					return
				}

				if err != nil {
					time.Sleep(s.config.SleepTime)
					attempts += 1
					s.logOnVerbose(logrus.ErrorLevel, "Queue %s - Receiver error (attempt %v/%v), unhandled error type: %s", queue, attempts, s.config.MaxSubRetries, err.Error())
					continue
				}
				defer releaseOnPanic(msg)

				//Notify subscriber about new message
				callback(queue, msg, nil)

				//reset attempts
				attempts = 0
			}

			receiver.Close(ctx)
		}

	}()

	s.logOnVerbose(logrus.InfoLevel, "Started listening to queue '%s'", queue)
	return subscriptionID, nil
}

func (s *Server) Unsubscribe(ID int) error {
	s.subscriptionLock.Lock()
	sub := s.subscriptions[ID]
	if sub == nil {
		s.subscriptionLock.Unlock()
		return ErrorSubscriptionDoesntExist
	}

	delete(s.subscriptions, ID)

	sub.cancelFunc()
	s.decrementWaitGroup()
	s.subscriptionLock.Unlock()

	return nil
}

func (s *Server) trackSubscription(sub *subscription) int {
	id := s.getId()

	s.subscriptionLock.Lock()
	s.subscriptions[id] = sub
	s.incrementWaitGroup()
	s.subscriptionLock.Unlock()

	return id
}
