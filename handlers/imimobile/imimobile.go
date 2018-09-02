package imimobile

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/utils"
	"github.com/pkg/errors"
	"net/http"
)

var sendURL = "https://cm.cloudev.com/entapi/SMS/sendMessage"

type handler struct {
	handlers.BaseHandler
}

/*
{
	"campaignId": "camp_001",
	"transId": "001_1235",
	"senderName": "FOOBAR",
	"priority": 1,
	"sendMessage": [{
		"msisdn": ["6666666666"],
		"msg": "Test",
		"countryCode": "66"
	}]
}*/

type imiPayload struct {
	CampaignId string `json:"campaignId" validate:"required"`
	TransId string `json:"transId" validate:"required"`
	SenderName string `json:"senderName" validate:"required"`
	Priority int `json:"priority"`
	SendMessage []sendMessage `json:"sendMessage" validate:"required"`
}

type sendMessage struct {
	Msisdn []string `json:"msisdn" validate:"required"`
	Msg string `json:"msg" validate:"required"`
	CountryCode string `json:"countryCode" validate:"required"`
}

func init() {
	courier.RegisterHandler(newHandler())
}

func newHandler() courier.ChannelHandler {
	return &handler{handlers.NewBaseHandler(courier.ChannelType("IMI"), "IMI Mobile")}
}

// Initialize is called by the engine once everything is loaded
func (h *handler) Initialize(s courier.Server) error {
	h.SetServer(s)
	s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveMessage)
	return nil
}

// receiveMessage is our HTTP handler function for incoming messages
func (h *handler) receiveMessage(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	fmt.Println("receiveMessage")
	payload := &imiPayload{}
	err := handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	// create our URN
	urn, err := handlers.StrictTelForCountry(payload.SendMessage[0].Msisdn[0], channel.Country())
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	// build our msg
	msg := h.Backend().NewIncomingMsg(channel, urn, payload.SendMessage[0].Msg)
	// and finally write our message
	return handlers.WriteMsgsAndResponse(ctx, h, []courier.Msg{msg}, w, r)
}

// SendMsg sends the passed in message, returning any error
func (h *handler) SendMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	username := msg.Channel().StringConfigForKey(courier.ConfigUsername, "")
	password := msg.Channel().StringConfigForKey(courier.ConfigPassword, "")
	apiKey := msg.Channel().StringConfigForKey(courier.ConfigAPIKey, "")
	msisdn := msg.URN().Path()
	transId := msg.ID().String()
	authorizationBase64 := base64.URLEncoding.EncodeToString([]byte(username + ":" + password))

	if username == "" {
		return nil, fmt.Errorf("no username set for IMI channel")
	}

	if password == "" {
		return nil, fmt.Errorf("no password set for IMI channel")
	}

	if apiKey == "" {
		return nil, fmt.Errorf("no api key set for IMI channel")
	}

	// build our request
	imiMsg := imiPayload{
		CampaignId: "camp_002", // TODO: get this information(where???)
		TransId:    transId,
		SenderName: "UNICEF", // TODO: get this information(where???)
		Priority:   0,
		SendMessage: []sendMessage{
			sendMessage{
				Msisdn:      []string{msisdn},
				Msg:         handlers.GetTextAndAttachments(msg),
				CountryCode: "55", // TODO: extract this information from msisdn
			},
		},
	}

	requestBody := &bytes.Buffer{}
	err := json.NewEncoder(requestBody).Encode(imiMsg)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequest(http.MethodPost, sendURL, requestBody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic " + authorizationBase64)
	req.Header.Set("Key", apiKey)

	rr, err := utils.MakeHTTPRequest(req)

	// record our status and log
	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgErrored)
	status.AddLog(courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), rr).WithError("Message Send Error", err))
	if err != nil {
		return status, nil
	}

	if rr.StatusCode/100 != 2 {
		return status, errors.Errorf("Got non-200 response [%d] from API", rr.StatusCode)
	}

	status.SetStatus(courier.MsgWired)

	return status, nil
}
