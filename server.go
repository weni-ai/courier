package courier

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"os"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"sync"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/courier/billing"
	"github.com/nyaruka/courier/metrics"
	"github.com/nyaruka/courier/templates"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/storage"
	"github.com/nyaruka/librato"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// Server is the main interface ChannelHandlers use to interact with backends. It provides an
// abstraction that makes mocking easier for isolated unit tests
type Server interface {
	Config() *Config

	AddHandlerRoute(handler ChannelHandler, method string, action string, handlerFunc ChannelHandleFunc)

	SendMsg(context.Context, Msg) (MsgStatus, error)

	Backend() Backend

	WaitGroup() *sync.WaitGroup
	StopChan() chan bool
	Stopped() bool

	Router() chi.Router

	Start() error
	Stop() error

	SetBilling(billing.Client)
	Billing() billing.Client

	Templates() templates.Client
	SetTemplates(templates templates.Client)

	GetHandler(channelType ChannelType) (ChannelHandler, error)
	SendMsgAction(ctx context.Context, msg Msg) (MsgStatus, error)
}

// NewServer creates a new Server for the passed in configuration. The server will have to be started
// afterwards, which is when configuration options are checked.
func NewServer(config *Config, backend Backend) Server {
	// create our top level router
	logger := logrus.New()
	return NewServerWithLogger(config, backend, logger)
}

// NewServerWithLogger creates a new Server for the passed in configuration. The server will have to be started
// afterwards, which is when configuration options are checked.
func NewServerWithLogger(config *Config, backend Backend, logger *logrus.Logger) Server {
	router := chi.NewRouter()
	router.Use(middleware.Compress(flate.DefaultCompression))
	router.Use(middleware.StripSlashes)
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(30 * time.Second))

	chanRouter := chi.NewRouter()
	router.Mount("/c/", chanRouter)

	return &server{
		config:  config,
		backend: backend,

		router:     router,
		chanRouter: chanRouter,

		stopChan:  make(chan bool),
		waitGroup: &sync.WaitGroup{},
		stopped:   false,
	}
}

// Start starts the Server listening for incoming requests and sending messages. It will return an error
// if it encounters any unrecoverable (or ignorable) error, though its bias is to move forward despite
// connection errors
func (s *server) Start() error {
	// set our user agent, needs to happen before we do anything so we don't change have threading issues
	utils.HTTPUserAgent = fmt.Sprintf("Courier/%s", s.config.Version)

	// configure librato if we have configuration options for it
	host, _ := os.Hostname()
	if s.config.LibratoUsername != "" {
		librato.Configure(s.config.LibratoUsername, s.config.LibratoToken, host, time.Second, s.waitGroup)
		librato.Start()
	}

	// start our backend
	err := s.backend.Start()
	if err != nil {
		return err
	}

	// start our spool flushers
	startSpoolFlushers(s)

	// wire up our main pages
	s.router.NotFound(s.handle404)
	s.router.MethodNotAllowed(s.handle405)
	s.router.Get("/", s.handleIndex)
	s.router.Get("/status", s.handleStatus)
	s.router.Get("/c/health", s.handleCHealth)
	s.router.Get("/c/metrics", promhttp.Handler().ServeHTTP)

	// initialize our handlers
	s.initializeChannelHandlers()

	// configure timeouts on our server
	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", s.config.Address, s.config.Port),
		Handler:      s.router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	// and start serving HTTP
	go func() {
		s.waitGroup.Add(1)
		defer s.waitGroup.Done()
		err := s.httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			logrus.WithFields(logrus.Fields{
				"comp":  "server",
				"state": "stopping",
				"err":   err,
			}).Error()
		}
	}()

	// start our heartbeat
	go func() {
		s.waitGroup.Add(1)
		defer s.waitGroup.Done()

		for !s.stopped {
			select {
			case <-s.stopChan:
				return
			case <-time.After(time.Minute):
				err := s.backend.Heartbeat()
				if err != nil {
					logrus.WithError(err).Error("error running backend heartbeat")
				}
			}
		}
	}()

	logrus.WithFields(logrus.Fields{
		"comp":    "server",
		"port":    s.config.Port,
		"state":   "started",
		"version": s.config.Version,
	}).Info("server listening on ", s.config.Port)

	// start our foreman for outgoing messages
	s.foreman = NewForeman(s, s.config.MaxWorkers)
	s.foreman.Start()

	return nil
}

// Stop stops the server, returning only after all threads have stopped
func (s *server) Stop() error {
	log := logrus.WithField("comp", "server")
	log.WithField("state", "stopping").Info("stopping server")

	// stop our foreman
	s.foreman.Stop()

	// shut down our HTTP server
	if err := s.httpServer.Shutdown(context.Background()); err != nil {
		log.WithField("state", "stopping").WithError(err).Error("error shutting down server")
	}

	// stop everything
	s.stopped = true
	close(s.stopChan)

	// stop our backend
	err := s.backend.Stop()
	if err != nil {
		return err
	}

	// stop our librato sender
	librato.Stop()

	// wait for everything to stop
	s.waitGroup.Wait()

	// clean things up, tearing down any connections
	s.backend.Cleanup()

	log.WithField("state", "stopped").Info("server stopped")
	return nil
}

func (s *server) SendMsg(ctx context.Context, msg Msg) (MsgStatus, error) {
	// find the handler for this message type
	handler, found := activeHandlers[msg.Channel().ChannelType()]
	if !found {
		return nil, fmt.Errorf("unable to find handler for channel type: %s", msg.Channel().ChannelType())
	}

	// have the handler send it
	return handler.SendMsg(ctx, msg)
}

func (s *server) WaitGroup() *sync.WaitGroup { return s.waitGroup }
func (s *server) StopChan() chan bool        { return s.stopChan }
func (s *server) Config() *Config            { return s.config }
func (s *server) Stopped() bool              { return s.stopped }

func (s *server) Backend() Backend   { return s.backend }
func (s *server) Router() chi.Router { return s.router }

func (s *server) Billing() billing.Client          { return s.billing }
func (s *server) SetBilling(client billing.Client) { s.billing = client }

func (s *server) Templates() templates.Client             { return s.templates }
func (s *server) SetTemplates(templates templates.Client) { s.templates = templates }

type server struct {
	backend Backend

	httpServer *http.Server
	router     *chi.Mux
	chanRouter *chi.Mux

	foreman *Foreman

	config *Config

	waitGroup *sync.WaitGroup
	stopChan  chan bool
	stopped   bool

	routes []string

	billing billing.Client

	templates templates.Client
}

func (s *server) initializeChannelHandlers() {
	includes := s.config.IncludeChannels
	excludes := s.config.ExcludeChannels

	// initialize handlers which are included/not-excluded in the config
	for _, handler := range registeredHandlers {
		channelType := string(handler.ChannelType())
		if (includes == nil || utils.StringArrayContains(includes, channelType)) && (excludes == nil || !utils.StringArrayContains(excludes, channelType)) {
			err := handler.Initialize(s)
			if err != nil {
				log.Fatal(err)
			}
			activeHandlers[handler.ChannelType()] = handler

			logrus.WithField("comp", "server").WithField("handler", handler.ChannelName()).WithField("handler_type", channelType).Info("handler initialized")
		}
	}

	// sort our route help
	sort.Strings(s.routes)
}

func (s *server) channelHandleWrapper(handler ChannelHandler, handlerFunc ChannelHandleFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// stuff a few things in our context that help with logging
		baseCtx := context.WithValue(r.Context(), contextRequestURL, r.URL.String())
		baseCtx = context.WithValue(baseCtx, contextRequestStart, time.Now())

		// Check immediately if this is an action request from context or URL
		// Actions should be processed but never logged/recorded in history
		if isAction := r.URL.Query().Get("is_action"); isAction == "true" {
			// Update context to flag this as an action
			baseCtx = context.WithValue(baseCtx, "is_action", true)
		}

		// add a 30 second timeout
		ctx, cancel := context.WithTimeout(baseCtx, time.Second*30)
		defer cancel()

		// If this is an action, we'll still process it but skip all logging
		if isAction, ok := ctx.Value("is_action").(bool); ok && isAction {
			// Get the channel to process the action
			channel, err := handler.GetChannel(ctx, r)
			if err != nil {
				// Just return success even if there's an error - we don't want anything recorded
				WriteStatusSuccess(ctx, w, r, nil)
				return
			}

			// Process action but discard the result - we don't want it logged
			r = r.WithContext(ctx)
			_, _ = handlerFunc(ctx, channel, w, r)

			// Return success without logging anything
			WriteStatusSuccess(ctx, w, r, nil)
			return
		}

		channel, err := handler.GetChannel(ctx, r)
		if err != nil {
			if err.Error() == "template update, so ignore" {
				WriteStatusSuccess(ctx, w, r, nil)
				return
			}
			WriteError(ctx, w, r, err)
			return
		}

		r = r.WithContext(ctx)

		// read the bytes from our body so we can create a channel log for this request
		response := &bytes.Buffer{}

		// Trim out cookie header, should never be part of authentication and can leak auth to channel logs
		r.Header.Del("Cookie")
		request, err := httputil.DumpRequest(r, true)
		if err != nil {
			writeAndLogRequestError(ctx, w, r, channel, err)
			return
		}
		url := fmt.Sprintf("https://%s%s", r.Host, r.URL.RequestURI())
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		ww.Tee(response)

		logs := make([]*ChannelLog, 0, 1)

		defer func() {
			// catch any panics and recover
			panicLog := recover()
			if panicLog != nil {
				debug.PrintStack()
				logrus.WithError(err).WithField("channel_uuid", channel.UUID()).WithField("url", url).WithField("request", string(request)).WithField("trace", panicLog).Error("panic handling request")
				writeAndLogRequestError(ctx, ww, r, channel, errors.New("panic handling msg"))
			}
		}()

		events, err := handlerFunc(ctx, channel, ww, r)
		duration := time.Now().Sub(start)
		secondDuration := float64(duration) / float64(time.Second)
		millisecondDuration := float64(duration) / float64(time.Millisecond)

		// if we received an error, write it out and report it
		if err != nil {
			// if error is from blocked contact message or invalid json received from too large message dont write it
			if !(err.Error() == "blocked contact sending message" || strings.Contains(err.Error(), "too large body")) {
				logrus.WithError(err).WithField("channel_uuid", channel.UUID()).WithField("url", url).WithField("request", string(request)).Error("error handling request")
				writeAndLogRequestError(ctx, ww, r, channel, err)
			}
		}

		// if we have a channel matched but no events were created we still want to log this to the channel, do so
		if channel != nil && len(events) == 0 {
			if err != nil {
				// if error is from blocked contact message or invalid json received from too large message return nothing
				if err.Error() == "blocked contact sending message" || strings.Contains(err.Error(), "too large body") {
					return
				} else {
					logs = append(logs, NewChannelLog("Channel Error", channel, NilMsgID, r.Method, url, ww.Status(), string(request), prependHeaders(response.String(), ww.Status(), w), duration, err))
					librato.Gauge(fmt.Sprintf("courier.channel_error_%s", channel.ChannelType()), secondDuration)
					metrics.SetChannelErrorByType(channel.ChannelType().String(), millisecondDuration)
					metrics.SetChannelErrorByUUID(channel.UUID().UUID, millisecondDuration)
				}
			} else {
				logs = append(logs, NewChannelLog("Request Ignored", channel, NilMsgID, r.Method, url, ww.Status(), string(request), prependHeaders(response.String(), ww.Status(), w), duration, err))
				librato.Gauge(fmt.Sprintf("courier.channel_ignored_%s", channel.ChannelType()), secondDuration)
				metrics.SetChannelIgnoredByType(channel.ChannelType().String(), millisecondDuration)
				metrics.SetChannelIgnoredByUUID(channel.UUID().UUID, millisecondDuration)
			}
		}

		// otherwise, log the request for each message
		for _, event := range events {
			switch e := event.(type) {
			case Msg:
				logs = append(logs, NewChannelLog("Message Received", channel, e.ID(), r.Method, url, ww.Status(), string(request), prependHeaders(response.String(), ww.Status(), w), duration, err))
				librato.Gauge(fmt.Sprintf("courier.msg_receive_%s", channel.ChannelType()), secondDuration)
				metrics.SetMsgReceiveByType(channel.ChannelType().String(), millisecondDuration)
				metrics.SetMsgReceiveByUUID(channel.UUID().UUID, millisecondDuration)
				LogMsgReceived(r, e)

				if err := handleBilling(s, e); err != nil {
					logrus.WithError(err).Info("Error handle billing on receive msg")
				}

			case ChannelEvent:
				logs = append(logs, NewChannelLog("Event Received", channel, NilMsgID, r.Method, url, ww.Status(), string(request), prependHeaders(response.String(), ww.Status(), w), duration, err))
				librato.Gauge(fmt.Sprintf("courier.evt_receive_%s", channel.ChannelType()), secondDuration)
				metrics.SetChannelEventReceiveByType(channel.ChannelType().String(), millisecondDuration)
				metrics.SetChannelEventReceiveByUUID(channel.UUID().UUID, millisecondDuration)
				LogChannelEventReceived(r, e)
			case MsgStatus:
				logs = append(logs, NewChannelLog("Status Updated", channel, e.ID(), r.Method, url, ww.Status(), string(request), response.String(), duration, err))
				librato.Gauge(fmt.Sprintf("courier.msg_status_%s", channel.ChannelType()), secondDuration)
				metrics.SetMsgStatusReceiveByType(channel.ChannelType().String(), millisecondDuration)
				metrics.SetMsgStatusReceiveByUUID(channel.UUID().UUID, millisecondDuration)
				LogMsgStatusReceived(r, e)
			}
		}

		// and write these out
		err = s.backend.WriteChannelLogs(ctx, logs)

		// log any error writing our channel log but don't break the request
		if err != nil {
			logrus.WithError(err).Error("error writing channel log")
		}
	}
}

func (s *server) AddHandlerRoute(handler ChannelHandler, method string, action string, handlerFunc ChannelHandleFunc) {
	method = strings.ToLower(method)
	channelType := strings.ToLower(string(handler.ChannelType()))

	path := fmt.Sprintf("/%s/{uuid:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}", channelType)
	if !handler.UseChannelRouteUUID() {
		path = fmt.Sprintf("/%s", channelType)
	}

	if action != "" {
		path = fmt.Sprintf("%s/%s", path, action)
	}
	s.chanRouter.Method(method, path, s.channelHandleWrapper(handler, handlerFunc))
	s.routes = append(s.routes, fmt.Sprintf("%-20s - %s %s", "/c"+path, handler.ChannelName(), action))
}

func prependHeaders(body string, statusCode int, resp http.ResponseWriter) string {
	output := &bytes.Buffer{}
	output.WriteString(fmt.Sprintf("HTTP/1.1 %d %s\r\n", statusCode, http.StatusText(statusCode)))
	resp.Header().Write(output)
	output.WriteString("\n")
	output.WriteString(body)
	return output.String()
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {

	var buf bytes.Buffer
	buf.WriteString("<title>courier</title><body><pre>\n")
	buf.WriteString(splash)
	buf.WriteString(s.config.Version)

	buf.WriteString(s.backend.Health())

	buf.WriteString("\n\n")
	buf.WriteString(strings.Join(s.routes, "\n"))
	buf.WriteString("</pre></body>")
	w.Write(buf.Bytes())
}

func (s *server) handle404(w http.ResponseWriter, r *http.Request) {
	logrus.WithField("url", r.URL.String()).WithField("method", r.Method).WithField("resp_status", "404").Info("not found")
	errors := []interface{}{NewErrorData(fmt.Sprintf("not found: %s", r.URL.String()))}
	err := WriteDataResponse(context.Background(), w, http.StatusNotFound, "Not Found", errors)
	if err != nil {
		logrus.WithError(err).Error()
	}
}

func (s *server) handle405(w http.ResponseWriter, r *http.Request) {
	logrus.WithField("url", r.URL.String()).WithField("method", r.Method).WithField("resp_status", "405").Info("invalid method")
	errors := []interface{}{NewErrorData(fmt.Sprintf("method not allowed: %s", r.Method))}
	err := WriteDataResponse(context.Background(), w, http.StatusMethodNotAllowed, "Method Not Allowed", errors)
	if err != nil {
		logrus.WithError(err).Error()
	}
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if s.config.StatusUsername != "" {
		user, pass, ok := r.BasicAuth()
		if !ok || user != s.config.StatusUsername || pass != s.config.StatusPassword {
			w.Header().Set("WWW-Authenticate", `Basic realm="Authenticate"`)
			w.WriteHeader(401)
			w.Write([]byte("Unauthorised.\n"))
			return
		}
	}

	var buf bytes.Buffer
	buf.WriteString("<title>courier</title><body><pre>\n")
	buf.WriteString(splash)
	buf.WriteString(s.config.Version)

	buf.WriteString("\n\n")
	buf.WriteString(s.backend.Status())
	buf.WriteString("\n\n")
	buf.WriteString("</pre></body>")
	w.Write(buf.Bytes())
}

func (s *server) handleCHealth(w http.ResponseWriter, r *http.Request) {
	healthcheck := NewHealthCheck()

	healthcheck.AddCheck("redis", s.CheckRedis)
	healthcheck.AddCheck("database", s.CheckDB)
	healthcheck.AddCheck("sentry", s.CheckSentry)
	healthcheck.AddCheck("s3", s.CheckS3)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*15)
	defer cancel()
	healthcheck.CheckUp(ctx)

	hsJSON, err := json.Marshal(healthcheck.HealthStatus)
	if err != nil {
		WriteDataResponse(context.Background(), w, http.StatusInternalServerError, "failed to marshal health status", []interface{}{err})
	}
	w.Write(hsJSON)
}

// for use in request.Context
type contextKey int

const (
	contextRequestURL contextKey = iota
	contextRequestStart
)

var splash = `
 ____________                   _____             
   ___  ____/_________  ___________(_)____________
    _  /  __  __ \  / / /_  ___/_  /_  _ \_  ___/
    / /__  / /_/ / /_/ /_  /   _  / /  __/  /    
    \____/ \____/\__,_/ /_/    /_/  \___//_/ v`

func (s *server) CheckRedis() error {
	rc := s.backend.RedisPool().Get()
	defer rc.Close()
	if _, err := rc.Do("PING"); err != nil {
		return fmt.Errorf("failed to ping redis: %s", err.Error())
	}
	return nil
}

func (s *server) CheckDB() error {
	db, err := sqlx.Open("postgres", s.config.DB)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %s", err.Error())
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("failed tot ping database: %s", err.Error())
	}
	return nil
}

func (s *server) CheckSentry() error {
	sentryDsn := s.config.SentryDSN
	if sentryDsn == "" {
		return errors.New("sentry dsn isn't configured")
	}
	return nil
}

func (s *server) CheckS3() error {
	var s3storage storage.Storage
	// create our storage (S3 or file system)
	if s.config.AWSAccessKeyID != "" {
		s3Client, err := storage.NewS3Client(&storage.S3Options{
			AWSAccessKeyID:     s.config.AWSAccessKeyID,
			AWSSecretAccessKey: s.config.AWSSecretAccessKey,
			Endpoint:           s.config.S3Endpoint,
			Region:             s.config.S3Region,
			DisableSSL:         s.config.S3DisableSSL,
			ForcePathStyle:     s.config.S3ForcePathStyle,
			MaxRetries:         3,
		})
		if err != nil {
			return err
		}
		s3storage = storage.NewS3(s3Client, s.config.S3MediaBucket, s.config.S3Region, 32)
	} else {
		return errors.New("s3 not configured")
	}

	// check our storage
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	err := s3storage.Test(ctx)
	cancel()
	if err != nil {
		return errors.New(s3storage.Name() + " S3 storage not available " + err.Error())
	}
	return nil
}

func handleBilling(s *server, msg Msg) error {
	billingMsg := billing.NewMessage(
		string(msg.URN().Identity()),
		"",
		msg.ContactName(),
		msg.Channel().UUID().String(),
		msg.ExternalID(),
		time.Now().Format(time.RFC3339),
		"I",
		msg.Channel().ChannelType().String(),
		msg.Text(),
		msg.Attachments(),
		msg.QuickReplies(),
		false,
		"",
		"",
	)
	billingMsg.ChannelType = string(msg.Channel().ChannelType())
	billingMsg.Text = msg.Text()
	billingMsg.Attachments = msg.Attachments()
	billingMsg.QuickReplies = msg.QuickReplies()

	if s.Billing() != nil {
		s.Billing().SendAsync(billingMsg, billing.RoutingKeyCreate, nil, nil)
	}

	return nil
}

func (s *server) SendMsgAction(ctx context.Context, msg Msg) (MsgStatus, error) {
	log := logrus.WithFields(logrus.Fields{
		"comp":        "server",
		"msg_id":      msg.ID().String(),
		"action_type": msg.ActionType(),
		"external_id": msg.ActionExternalID(),
	})
	if msg.Channel() == nil {
		err := errors.New("cannot send message action: message channel is nil")
		log.WithError(err).Error("Failed")
		return nil, err
	}
	log = log.WithField("channel_uuid", msg.Channel().UUID())

	handler, err := s.GetHandler(msg.Channel().ChannelType())
	if err != nil {
		log.WithError(err).Error("Handler not found")
		return nil, err
	}

	if actionHandler, ok := handler.(ActionSender); ok {
		log.Infof("Dispatching action to handler %s via ActionSender interface", handler.ChannelName())
		// Set a flag in the context to indicate this is an action
		ctx = context.WithValue(ctx, "is_action", true)
		_, err := actionHandler.SendAction(ctx, msg)
		return nil, err
	}

	err = fmt.Errorf("handler %s (%T) does not support actions", handler.ChannelName(), handler)
	log.Warn("Action not supported by handler")
	return nil, err
}

func (s *server) GetHandler(channelType ChannelType) (ChannelHandler, error) {
	handler, found := activeHandlers[channelType]
	if !found {
		return nil, fmt.Errorf("no active handler found for channel type: %s", channelType)
	}
	return handler, nil
}
