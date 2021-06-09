package amq

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/Azure/go-amqp"
	"github.com/sirupsen/logrus"
	"os"
	"os/signal"
	"sync"
	"time"
)

var (
	ErrorNotConnected = fmt.Errorf("not connected to Server")
	ErrorNoBrokers    = fmt.Errorf("no Brokers provided")
)

type Config struct {
	ConnTimeout   time.Duration `yaml:"connectionTimeout" json:"connectionTimeout"`
	TLS           *tls.Config   `yaml:"tls" json:"tls"`
	Brokers       []string      `yaml:"brokers" json:"brokers"`
	Verbose       bool          `yaml:"verbose" json:"verbose"`
	SleepTime     time.Duration `yaml:"sleepTime" json:"sleepTime"`
	MaxSubRetries int           `yaml:"maxSubscriptionRetries" json:"maxSubscriptionRetries"`
	MaxPubRetries int           `yaml:"maxPublishRetries" json:"maxPublishRetries"`
}

type Server struct {
	config Config

	connected bool
	context   context.Context

	connSession *amqp.Session
	client      *amqp.Client
	connLock    sync.Mutex
	done        bool

	contextWaitgroup     sync.WaitGroup
	contextWaitgroupLock sync.Mutex

	subscriptionLock      sync.Mutex
	subscriptions         map[int]*subscription
	subscriptionIdCounter int

	senders    map[string]*amqp.Sender
	senderLock sync.Mutex
}

func (s *Server) Connected() bool {
	return s.connected
}

func (s *Server) getId() (id int) {
	id = s.subscriptionIdCounter
	s.subscriptionIdCounter++
	return
}

func (s *Server) logOnVerbose(level logrus.Level, format string, args ...interface{}) {
	if s.config.Verbose {
		switch level {
		case logrus.DebugLevel:
			logrus.Debugf(format, args...)
		case logrus.InfoLevel:
			logrus.Infof(format, args...)
		case logrus.WarnLevel:
			logrus.Warnf(format, args...)
		case logrus.ErrorLevel:
			logrus.Errorf(format, args...)
		}
	}
}

func (s *Server) getSender(queue string, options ...amqp.LinkOption) (sender *amqp.Sender, err error) {
	if !s.connected {
		return nil, ErrorNotConnected
	}

	s.senderLock.Lock()
	defer s.senderLock.Unlock()

	sender = s.senders[queue]

	if sender != nil {
		return sender, nil
	}

	sender, err = s.connSession.NewSender(options...)
	s.senders[queue] = sender
	return sender, err
}

func (s *Server) getReceiver(options ...amqp.LinkOption) (*amqp.Receiver, error) {
	if !s.connected {
		return nil, ErrorNotConnected
	}

	receiver, err := s.connSession.NewReceiver(options...)
	return receiver, err
}

func (s *Server) connect() error {
	logrus.Info("Trying to open activemq connection...")
	if s.connSession != nil {
		s.logOnVerbose(logrus.InfoLevel, "closing existing session")
		s.connSession.Close(s.context)
	}

	s.senderLock.Lock()
	s.senders = map[string]*amqp.Sender{}
	s.senderLock.Unlock()

	connOptions := []amqp.ConnOption{
		amqp.ConnConnectTimeout(time.Second * 5),
		amqp.ConnIdleTimeout(0),
	}

	if s.config.TLS != nil {
		logrus.Info("Running with TLS enabled")
		connOptions = append(
			connOptions,
			amqp.ConnTLS(true),
			amqp.ConnTLSConfig(s.config.TLS),
		)
	} else {
		connOptions = append(connOptions, amqp.ConnSASLAnonymous())
	}

outer:
	for s.context.Err() == nil && !s.done {
		var err error
		var client *amqp.Client
		//Try to connect to provided Brokers
		for _, broker := range s.config.Brokers {
			s.logOnVerbose(logrus.InfoLevel, "Trying to connect to broker '%s'", broker)

			client, err = amqp.Dial("amqp://"+broker+"?transport.transformer=jms", connOptions...)

			//Accept first available connection
			if err == nil {
				s.logOnVerbose(logrus.InfoLevel, "Successfully connected to broker '%s'", broker)
				break
			}

			s.logOnVerbose(logrus.WarnLevel, "Failed to connect to broker '%s': %s", broker, err.Error())
		}

		//If err is not nil, no connections were available
		if err != nil {
			time.Sleep(s.config.SleepTime)
			continue outer
		}

		//Create an amqp connSession
		session, err := client.NewSession()
		if err != nil || s.context.Err() != nil {
			client.Close()
			time.Sleep(s.config.SleepTime)
			continue outer
		}

		s.client = client
		s.connSession = session
		s.connected = true
		return nil
	}
	return s.context.Err()
}

func (s *Server) incrementWaitGroup() {
	s.contextWaitgroupLock.Lock()
	s.contextWaitgroup.Add(1)
	s.contextWaitgroupLock.Unlock()
}

func (s *Server) decrementWaitGroup() {
	s.contextWaitgroupLock.Lock()
	s.contextWaitgroup.Done()
	s.contextWaitgroupLock.Unlock()
}

//StayAlive - wait for context to end.
func (s *Server) StayAlive() {
	ctx, cancel := context.WithCancel(s.context)
	select {
	case <-ctx.Done():
		cancel()
		return
	}
	cancel()
	return
}

func heartbeat(s *Server) {
	ticker := time.NewTicker(time.Second * 5)

	go func() {
		for range ticker.C {
			if s.context.Err() != nil || s.done {
				logrus.Infof("Detected context end - heartbeat stopping")
				ticker.Stop()
				return
			}

			rec, err := s.connSession.NewReceiver(amqp.LinkSourceAddress("system.heartbeat"))
			if err != nil {
				logrus.Warnf("Heartbeat failed - connection gone bad? Reconnecting...")

				//On context end
				if s.connect() != nil {
					return
				}
			} else {
				rec.Close(s.context)
			}
		}
	}()
}

func (s *Server) killSenders() int {
	closed := 0
	for queue, sender := range s.senders {
		_ = sender.Close(s.context)
		logrus.Infof("Closed sender on queue %s", queue)
		closed++
	}
	return closed
}

func (s *Server) shutdownOnContextEndOrSig() {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc,
		os.Interrupt,
		os.Kill,
	)
	defer signal.Stop(sigc)

	//Wait for sub processes to end before quitting.
	select {
	case <-s.context.Done():
		s.done = true
		logrus.Info("Canceled")
		s.senderLock.Lock()
		s.connLock.Lock()
		s.contextWaitgroup.Wait()
		_ = s.connSession.Close(s.context)
		_ = s.client.Close()
		_ = s.killSenders()
		logrus.Info("exited cleanly")
	case sig := <-sigc:
		logrus.Info("Sigcd - signal:", sig)
		s.done = true
		s.senderLock.Lock()
		s.connLock.Lock()
		for id := range s.subscriptions {
			s.Unsubscribe(id)
		}
		s.contextWaitgroup.Wait()
		_ = s.connSession.Close(s.context)
		_ = s.client.Close()
		_ = s.killSenders()
		logrus.Info("exited cleanly")
		os.Exit(0)

	}
}

//NewServer - creates a new amq S erver and connects to one of provided Brokers
func NewServer(ctx context.Context, brokers []string, options ...ServerOption) (*Server, error) {
	if len(brokers) == 0 {
		return nil, ErrorNoBrokers
	}

	amq := Server{
		context: ctx,

		config: Config{
			Brokers:       brokers,
			SleepTime:     time.Second * 3,
			MaxSubRetries: 3,
			MaxPubRetries: 3,
		},

		subscriptions: make(map[int]*subscription),
		senders:       map[string]*amqp.Sender{},

		contextWaitgroup: sync.WaitGroup{},
	}

	for _, option := range options {
		option(&amq)
	}

	err := amq.connect()
	heartbeat(&amq)
	go amq.shutdownOnContextEndOrSig()
	return &amq, err
}

//NewServer - creates a new amq Server and connects to one of provided Brokers
func NewServerWithConfig(ctx context.Context, config Config) (*Server, error) {
	if len(config.Brokers) == 0 {
		return nil, ErrorNoBrokers
	}

	if config.MaxPubRetries == 0 {
		logrus.Info("MaxPubRetries was 0, defaulting to 3")
		config.MaxPubRetries = 3
	}

	if config.MaxSubRetries == 0 {
		logrus.Info("MaxSubRetries was 0, defaulting to 3")
		config.MaxSubRetries = 3
	}

	if config.SleepTime == 0 {
		logrus.Info("Sleeptime was 0, defaulting to 3s")
		config.SleepTime = 3 * time.Second
	}

	amq := Server{
		context: ctx,

		config: config,

		subscriptions: make(map[int]*subscription),
		senders:       map[string]*amqp.Sender{},

		contextWaitgroup: sync.WaitGroup{},
	}

	err := amq.connect()
	heartbeat(&amq)
	go amq.shutdownOnContextEndOrSig()
	return &amq, err
}
