package courier

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/buger/jsonparser"
	"github.com/nyaruka/courier/billing"
	"github.com/nyaruka/courier/templates"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/urns"
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
	log := logrus.WithField("comp", "sender").WithField("sender_id", w.id)
	if msg.Channel() != nil {
		log = log.WithField("channel_uuid", msg.Channel().UUID())
	} else {
		log.Warn("Message channel is nil at the start of sendMessage")
	}

	var status MsgStatus
	server := w.foreman.server
	backend := server.Backend()

	dbOpCtx, dbOpCancel := context.WithTimeout(context.Background(), time.Second*35)
	defer dbOpCancel()

	if msg.ID() != NilMsgID {
		log = log.WithField("msg_id", msg.ID().String())
	}
	if msg.URN() != urns.NilURN {
		log = log.WithField("msg_urn", msg.URN().Identity())
	}
	if msg.Text() != "" {
		log = log.WithField("msg_text", msg.Text())
	}
	if len(msg.Attachments()) > 0 {
		log = log.WithField("attachments", msg.Attachments())
	}
	if len(msg.QuickReplies()) > 0 {
		log = log.WithField("quick_replies", msg.QuickReplies())
	}

	start := time.Now()

	// if this is a resend, clear our sent status
	if msg.IsResend() {
		err := backend.ClearMsgSent(dbOpCtx, msg.ID())
		if err != nil {
			log.WithError(err).Error("error clearing sent status for msg")
		}
	}

	// was this msg already sent? (from a double queue?)
	sent, err := backend.WasMsgSent(dbOpCtx, msg.ID())
	if err != nil {
		log.WithError(err).Error("error looking up msg was sent")
	}

	// is this msg in a loop?
	loop, err := backend.IsMsgLoop(dbOpCtx, msg)
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
		// Message is not a duplicate and not in a loop
		actionType := msg.ActionType()

		if actionType == MsgActionTypingIndicator {
			// --- HANDLE MESSAGE ACTION ---
			actionLog := log.WithField("action_type", actionType)
			actionLog.Info("Processing message action")

			actionCallCtx, actionCallCancel := context.WithTimeout(context.Background(), time.Second*20) // Context for the action call
			defer actionCallCancel()

			var actionErr error
			status, actionErr = server.SendMsgAction(actionCallCtx, msg)

			if actionErr != nil {
				actionLog.WithError(actionErr).Error("Error processing message action")
				if status == nil {
					if msg.Channel() != nil && msg.ID() != NilMsgID {
						status = backend.NewMsgStatusForID(msg.Channel(), msg.ID(), MsgErrored)
						status.AddLog(NewChannelLogFromError("Action Processing Error", msg.Channel(), msg.ID(), 0, actionErr))
					} else {
						actionLog.Error("Cannot create error status for action: channel or msg ID is nil")
					}
				}
			} else {
				actionLog.Info("Message action processed successfully")
				if status == nil {
					actionLog.Warn("SendMsgAction returned nil status and nil error. Creating MsgWired status.")
					if msg.Channel() != nil && msg.ID() != NilMsgID {
						status = backend.NewMsgStatusForID(msg.Channel(), msg.ID(), MsgWired)
					}
				}
			}

		} else {
			// --- HANDLE NORMAL MESSAGE SENDING ---
			waitMediaChannels := w.foreman.server.Config().WaitMediaChannels
			msgChannelTypeStr := ""
			if msg.Channel() != nil {
				msgChannelTypeStr = msg.Channel().ChannelType().String()
			}
			mustWait := utils.StringArrayContains(waitMediaChannels, msgChannelTypeStr)

			if mustWait && msg.Channel() != nil { // Ensure channel is not nil for this block
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
							prevMsgCtx, prevMsgCancel := context.WithTimeout(context.Background(), time.Second*35)
							defer prevMsgCancel()
							previousEventMsgUUID := msgEvents[msgIndex-1].Msg.UUID
							tries := 0
							tryLimit := w.foreman.server.Config().WaitMediaCount
							for tries < tryLimit {
								tries++
								prevMsg, err := server.Backend().GetMessage(prevMsgCtx, previousEventMsgUUID)
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

			sendCallCtx, sendCallCancel := context.WithTimeout(context.Background(), time.Second*35)
			defer sendCallCancel()

			// send our message
			var sendErr error
			status, sendErr = server.SendMsg(sendCallCtx, msg)
			duration := time.Now().Sub(start)
			secondDuration := float64(duration) / float64(time.Second)

			if sendErr != nil {
				log.WithError(sendErr).WithField("elapsed", duration).Error("error sending message")
				if status == nil {
					if msg.Channel() != nil && msg.ID() != NilMsgID {
						status = backend.NewMsgStatusForID(msg.Channel(), msg.ID(), MsgErrored)
						status.AddLog(NewChannelLogFromError("Sending Error", msg.Channel(), msg.ID(), duration, sendErr))
					} else {
						log.Error("Cannot create error status for send: channel or msg ID is nil")
					}
				}
			}

			// report to librato and log locally
			if status != nil {
				if status.Status() == MsgErrored || status.Status() == MsgFailed {
					log.WithField("elapsed", duration).Warning("msg errored")
					if msg.Channel() != nil {
						librato.Gauge(fmt.Sprintf("courier.msg_send_error_%s", msg.Channel().ChannelType()), secondDuration)
					}
				} else {
					log.WithField("elapsed", duration).Info("msg sent")
					if msg.Channel() != nil {
						librato.Gauge(fmt.Sprintf("courier.msg_send_%s", msg.Channel().ChannelType()), secondDuration)
					}
				}

				sentOk := status.Status() != MsgErrored && status.Status() != MsgFailed
				if sentOk {
					if w.foreman.server.Billing() != nil && msg.Channel() != nil {
						chatsUUID, _ := jsonparser.GetString(msg.Metadata(), "chats_msg_uuid")
						if msg.Channel().ChannelType() != "WAC" || chatsUUID != "" { // if message is not to a WAC channel or is from a wenichats agent then send to exchange
							ticketerType, _ := jsonparser.GetString(msg.Metadata(), "ticketer_type")
							fromTicketer := ticketerType != ""

							billingMsg := billing.NewMessage(
								string(msg.URN().Identity()),
								"",
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
								"",
							)
							routingKey := billing.RoutingKeyCreate
							if msg.Channel().ChannelType() == "WAC" {
								routingKey = billing.RoutingKeyWAC
							}
							w.foreman.server.Billing().SendAsync(billingMsg, routingKey, nil, nil)
						}
					}

					if w.foreman.server.Templates() != nil {

						mdJSON := msg.Metadata()
						metadata := &templates.TemplateMetadata{}
						err := json.Unmarshal(mdJSON, metadata)
						if err != nil {
							log.WithError(err).Error("error unmarshalling metadata")
						}
						templatingData := metadata.Templating
						if templatingData == nil {
							log.Error("templating data is nil")
						}

						if err == nil && templatingData != nil {
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
			} else {
				log.Error("Status is nil after SendMsg call, cannot report to librato or billing.")
			}
		}
	}

	// --- COMMON FINALIZATION ---
	// Ensure status is not nil before proceeding
	if status == nil {
		log.Error("CRITICAL: Status is nil before finalization block. Attempting to create a fallback error status.")
		if msg.Channel() != nil && msg.ID() != NilMsgID {
			status = backend.NewMsgStatusForID(msg.Channel(), msg.ID(), MsgErrored)
			status.AddLog(NewChannelLogFromError("Internal Sender Error", msg.Channel(), msg.ID(), 0, errors.New("status was nil before finalization")))
		} else {
			log.Error("Cannot create fallback error status: channel or msg ID is nil. Skipping finalization for this message.")
			return
		}
	}

	// dbOpCtx is still valid here
	writeStatusErr := backend.WriteMsgStatus(dbOpCtx, status)
	if writeStatusErr != nil {
		log.WithError(writeStatusErr).Info("error writing msg status")
	}

	// write our logs as well
	if len(status.Logs()) > 0 {
		writeLogsErr := backend.WriteChannelLogs(dbOpCtx, status.Logs())
		if writeLogsErr != nil {
			log.WithError(writeLogsErr).Info("error writing msg logs")
		}
	}

	// write our contact last seen only for normal messages that were processed
	// (not for actions we sent, and ensure status indicates it wasn't an initial error like loop/duplicate)
	if msg.ActionType() == MsgActionNone && status.Status() != MsgFailed && status.Status() != MsgWired {
		if msg.Channel() != nil { // Ensure channel is not nil
			lastSeenErr := backend.WriteContactLastSeen(dbOpCtx, msg, time.Now())
			if lastSeenErr != nil {
				log.WithError(lastSeenErr).Info("error writing contact last seen")
			}
		}
	}

	// mark our send task as complete
	if msg.ID() != NilMsgID { // Ensure ID is valid
		backend.MarkOutgoingMsgComplete(dbOpCtx, msg, status)
	} else {
		log.Error("Cannot mark outgoing message complete: msg ID is nil.")
	}
}
