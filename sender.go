package courier

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/buger/jsonparser"
	"github.com/nyaruka/courier/billing"
	"github.com/nyaruka/courier/metrics"
	"github.com/nyaruka/courier/templates"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/librato"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// Foreman takes care of managing our set of sending workers and assigns msgs for each to send
type Foreman struct {
	server           Server
	senders          []*Sender
	availableSenders chan *Sender
	quit             chan bool
}

// NewForeman creates a new Foreman for the passed in server with the number of max senders
func NewForeman(server Server, maxSenders int) *Foreman {
	foreman := &Foreman{
		server:           server,
		senders:          make([]*Sender, maxSenders),
		availableSenders: make(chan *Sender, maxSenders),
		quit:             make(chan bool),
	}

	for i := 0; i < maxSenders; i++ {
		foreman.senders[i] = NewSender(foreman, i)
	}

	return foreman
}

// Start starts the foreman and all its senders, assigning jobs while there are some
func (f *Foreman) Start() {
	metrics.SetAvailableWorkers(len(f.senders))
	metrics.SetUsedWorkers(0)

	for _, sender := range f.senders {
		sender.Start()
	}
	go f.Assign()
}

// Stop stops the foreman and all its senders, the wait group of the server can be used to track progress
func (f *Foreman) Stop() {
	for _, sender := range f.senders {
		sender.Stop()
	}
	close(f.quit)
	logrus.WithField("comp", "foreman").WithField("state", "stopping").Info("foreman stopping")

	metrics.SetUsedWorkers(0)
	metrics.SetAvailableWorkers(0)
}

// Assign is our main loop for the Foreman, it takes care of popping the next outgoing messages from our
// backend and assigning them to workers
func (f *Foreman) Assign() {
	f.server.WaitGroup().Add(1)
	defer f.server.WaitGroup().Done()
	log := logrus.WithField("comp", "foreman")

	log.WithFields(logrus.Fields{
		"state":   "started",
		"senders": len(f.senders),
	}).Info("senders started and waiting")

	backend := f.server.Backend()
	lastSleep := false

	go f.RecordWorkerMetrics()

	for true {
		select {
		// return if we have been told to stop
		case <-f.quit:
			log.WithField("state", "stopped").Info("foreman stopped")
			return

		// otherwise, grab the next msg and assign it to a sender
		case sender := <-f.availableSenders:
			// see if we have a message to work on
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
			msg, err := backend.PopNextOutgoingMsg(ctx)
			cancel()

			if err == nil && msg != nil {
				// if so, assign it to our sender
				sender.job <- msg
				lastSleep = false
			} else {
				// we received an error getting the next message, log it
				if err != nil {
					log.WithError(err).Error("error popping outgoing msg")
				}

				// add our sender back to our queue and sleep a bit
				if !lastSleep {
					log.Debug("sleeping, no messages")
					lastSleep = true
				}
				f.availableSenders <- sender
				time.Sleep(250 * time.Millisecond)
			}
		}
	}
}

func (f *Foreman) RecordWorkerMetrics() {
	for {
		metrics.SetAvailableWorkers(len(f.availableSenders))
		metrics.SetUsedWorkers(len(f.senders) - len(f.availableSenders))
		time.Sleep(1 * time.Second)
	}
}

// Sender is our type for a single goroutine that is sending messages
type Sender struct {
	id      int
	foreman *Foreman
	job     chan Msg
	log     *logrus.Entry
}

// NewSender creates a new sender responsible for sending messages
func NewSender(foreman *Foreman, id int) *Sender {
	sender := &Sender{
		id:      id,
		foreman: foreman,
		job:     make(chan Msg, 1),
	}
	return sender
}

// Start starts our Sender's goroutine and has it start waiting for tasks from the foreman
func (w *Sender) Start() {
	go func() {
		w.foreman.server.WaitGroup().Add(1)
		defer w.foreman.server.WaitGroup().Done()

		log := logrus.WithField("comp", "sender").WithField("sender_id", w.id)
		log.Debug("started")

		for true {
			// list ourselves as available for work
			w.foreman.availableSenders <- w

			// grab our next piece of work
			msg := <-w.job

			// exit if we were stopped
			if msg == nil {
				log.Debug("stopped")
				return
			}

			w.sendMessage(msg)
		}
	}()
}

// Stop stops our senders, callers can use the server's wait group to track progress
func (w *Sender) Stop() {
	close(w.job)
}

func (w *Sender) sendMessage(msg Msg) {
	// --- HANDLE MESSAGE ACTION (non-blocking) ---
	if msg.ActionType() == MsgActionTypingIndicator {
		actionCallCtx, actionCallCancel := context.WithTimeout(context.Background(), time.Second*20)
		defer actionCallCancel()

		// Set a flag in the context to indicate this is an action
		actionCallCtx = context.WithValue(actionCallCtx, "is_action", true)

		_, err := w.foreman.server.SendMsgAction(actionCallCtx, msg)
		if err != nil {
			// Log the error but don't interrupt the flow - typing indicator is non-critical
			logrus.WithFields(logrus.Fields{
				"comp":        "sender",
				"sender_id":   w.id,
				"action_type": msg.ActionType(),
				"external_id": msg.ActionExternalID(),
				"error":       err.Error(),
			}).Warn("typing indicator action failed (non-critical, continuing flow)")
		}
		return
	}

	log := logrus.WithField("comp", "sender").WithField("sender_id", w.id).WithField("channel_uuid", msg.Channel().UUID())

	var status MsgStatus
	server := w.foreman.server
	backend := server.Backend()

	// we don't want any individual send taking more than 35s
	sendCTX, cancel := context.WithTimeout(context.Background(), time.Second*35)
	defer cancel()

	log = log.WithField("msg_id", msg.ID().String()).WithField("msg_text", msg.Text()).WithField("msg_urn", msg.URN().Identity())
	if len(msg.Attachments()) > 0 {
		log = log.WithField("attachments", msg.Attachments())
	}
	if len(msg.QuickReplies()) > 0 {
		log = log.WithField("quick_replies", msg.QuickReplies())
	}

	start := time.Now()

	// if this is a resend, clear our sent status
	if msg.IsResend() {
		err := backend.ClearMsgSent(sendCTX, msg.ID())
		if err != nil {
			log.WithError(err).Error("error clearing sent status for msg")
		}

	}

	// was this msg already sent? (from a double queue?)
	sent, err := backend.WasMsgSent(sendCTX, msg.ID())

	// failing on a lookup isn't a halting problem but we should log it
	if err != nil {
		log.WithError(err).Error("error looking up msg was sent")
	}

	// is this msg in a loop?
	loop, err := backend.IsMsgLoop(sendCTX, msg)

	// failing on loop lookup isn't permanent, but log
	if err != nil {
		log.WithError(err).Error("error looking up msg loop")
	}

	if sent {
		// if this message was already sent, create a wired status for it
		status = backend.NewMsgStatusForID(msg.Channel(), msg.ID(), MsgWired)
		log.Warning("duplicate send, marking as wired")
	} else if loop {
		// if this contact is in a loop, fail the message immediately without sending
		status = backend.NewMsgStatusForID(msg.Channel(), msg.ID(), MsgFailed)
		status.AddLog(NewChannelLogFromError("Message Loop", msg.Channel(), msg.ID(), 0, fmt.Errorf("message loop detected, failing message without send")))
		log.Error("message loop detected, failing message")
	} else {

		waitMediaChannels := w.foreman.server.Config().WaitMediaChannels
		msgChannel := msg.Channel().ChannelType().String()
		mustWait := utils.StringArrayContains(waitMediaChannels, msgChannel)

		if mustWait {
			// check if previous message is already Delivered
			msgUUID := msg.UUID().String()

			if msgUUID != "" {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second*35)
				defer cancel()

				msgEvents, err := server.Backend().GetRunEventsByMsgUUIDFromDB(ctx, msgUUID)

				if err != nil {
					log.Error(errors.Wrap(err, "unable to get events"))
				}

				if msgEvents != nil {

					msgIndex := func(slice []RunEvent, item string) int {
						for i := range slice {
							if slice[i].Msg.UUID == item {
								return i
							}
						}
						return -1
					}(msgEvents, msg.UUID().String())

					if msgIndex > 0 {
						ctx, cancel := context.WithTimeout(context.Background(), time.Second*35)
						defer cancel()
						previousEventMsgUUID := msgEvents[msgIndex-1].Msg.UUID
						tries := 0
						tryLimit := w.foreman.server.Config().WaitMediaCount
						for tries < tryLimit {
							tries++
							prevMsg, err := server.Backend().GetMessage(ctx, previousEventMsgUUID)
							if err != nil {
								log.Error(errors.Wrap(err, "GetMessage for previous message failed"))
								break
							}
							if prevMsg != nil {
								if prevMsg.Status() != MsgDelivered &&
									prevMsg.Status() != MsgRead {
									sleepDuration := time.Duration(w.foreman.server.Config().WaitMediaSleepDuration)
									time.Sleep(time.Millisecond * sleepDuration)
									continue
								}
							}
							break
						}
					}
				}
			}
		}

		nsendCTX, ncancel := context.WithTimeout(context.Background(), time.Second*35)
		defer ncancel()
		// send our message
		status, err = server.SendMsg(nsendCTX, msg)
		duration := time.Now().Sub(start)
		secondDuration := float64(duration) / float64(time.Second)
		millisecondDuration := float64(duration) / float64(time.Millisecond)

		if err != nil {
			log.WithError(err).WithField("elapsed", duration).Error("error sending message")
			if status == nil {
				status = backend.NewMsgStatusForID(msg.Channel(), msg.ID(), MsgErrored)
				status.AddLog(NewChannelLogFromError("Sending Error", msg.Channel(), msg.ID(), duration, err))
			}
		}

		// report to librato and log locally
		if status.Status() == MsgErrored || status.Status() == MsgFailed {
			log.WithField("elapsed", duration).Warning("msg errored")
			librato.Gauge(fmt.Sprintf("courier.msg_send_error_%s", msg.Channel().ChannelType()), secondDuration)
			metrics.SetMsgSendErrorByType(msg.Channel().ChannelType().String(), millisecondDuration)
			metrics.SetMsgSendErrorByUUID(msg.Channel().UUID().UUID, millisecondDuration)
		} else {
			log.WithField("elapsed", duration).Info("msg sent")
			librato.Gauge(fmt.Sprintf("courier.msg_send_%s", msg.Channel().ChannelType()), secondDuration)
			metrics.SetMsgSendSuccessByType(msg.Channel().ChannelType().String(), millisecondDuration)
			metrics.SetMsgSendSuccessByUUID(msg.Channel().UUID().UUID, millisecondDuration)
		}

		sentOk := status.Status() != MsgErrored && status.Status() != MsgFailed
		if sentOk {
			isTemplateMessage, metadata := isTemplateMessage(msg)

			if w.foreman.server.Billing() != nil {
				chatsUUID, _ := jsonparser.GetString(msg.Metadata(), "chats_msg_uuid")
				ticketerType, _ := jsonparser.GetString(msg.Metadata(), "ticketer_type")
				fromTicketer := ticketerType != ""

				billingMsg := billing.NewMessage(
					string(msg.URN().Identity()),
					"",
					msg.ContactName(),
					msg.Channel().UUID().String(),
					status.ExternalID(),
					time.Now().Format(time.RFC3339),
					"O",
					msg.Channel().ChannelType().String(),
					msg.Text(),
					msg.Attachments(),
					msg.QuickReplies(),
					fromTicketer,
					chatsUUID,
					string(msg.Status()),
				)

				if isTemplateMessage {
					billingMsg.TemplateUUID = metadata.Templating.Template.UUID
				}

				if msg.Channel().ChannelType() == "WAC" {
					w.foreman.server.Billing().SendAsync(billingMsg, billing.RoutingKeyWAC, nil, nil)
				}
				w.foreman.server.Billing().SendAsync(billingMsg, billing.RoutingKeyCreate, nil, nil)
			}

			if w.foreman.server.Templates() != nil && isTemplateMessage {
				templatingData := metadata.Templating
				templateName := templatingData.Template.Name
				templateUUID := templatingData.Template.UUID
				templateLanguage := templatingData.Language
				templateNamespace := templatingData.Namespace

				var templateVariables []string
				if templatingData.Variables != nil {
					templateVariables = templatingData.Variables
				}

				templateMsg := templates.NewTemplateMessage(
					string(msg.URN().Identity()),
					"",
					msg.Channel().UUID().String(),
					status.ExternalID(),
					time.Now().Format(time.RFC3339),
					"O",
					msg.Channel().ChannelType().String(),
					msg.Text(),
					templateName,
					templateUUID,
					templateLanguage,
					templateNamespace,
					templateVariables,
				)
				w.foreman.server.Templates().SendAsync(templateMsg, templates.RoutingKeySend, nil, nil)
			}
		}
	}

	// we allot 15 seconds to write our status to the db
	writeCTX, cancel := context.WithTimeout(context.Background(), time.Second*15)
	defer cancel()

	err = backend.WriteMsgStatus(writeCTX, status)
	if err != nil {
		log.WithError(err).Info("error writing msg status")
	}

	// write our logs as well
	err = backend.WriteChannelLogs(writeCTX, status.Logs())
	if err != nil {
		log.WithError(err).Info("error writing msg logs")
	}

	// mark our send task as complete
	backend.MarkOutgoingMsgComplete(writeCTX, msg, status)
}

// isTemplateMessage checks if a message contains valid template metadata
func isTemplateMessage(msg Msg) (bool, *templates.TemplateMetadata) {
	if msg.Metadata() == nil {
		return false, nil
	}

	mdJSON := msg.Metadata()
	metadata := &templates.TemplateMetadata{}
	err := json.Unmarshal(mdJSON, metadata)
	if err != nil {
		return false, nil
	}

	// Check if templating data exists and has required fields
	if metadata.Templating == nil {
		return false, metadata
	}

	// Verify that essential template fields are present
	templating := metadata.Templating
	if templating.Template.Name == "" || templating.Template.UUID == "" || templating.Language == "" {
		return false, metadata
	}

	return true, metadata
}
