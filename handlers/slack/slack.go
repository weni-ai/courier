package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/buger/jsonparser"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/urns"
	"github.com/pkg/errors"
)

var apiURL = "https://slack.com/api"

const (
	configBotToken        = "bot_token"
	configUserToken       = "user_token"
	configValidationToken = "verification_token"
)

var (
	ErrAlreadyPublic         = "already_public"
	ErrPublicVideoNotAllowed = "public_video_not_allowed"
)

func init() {
	courier.RegisterHandler(newHandler())
}

type handler struct {
	handlers.BaseHandler
}

func newHandler() courier.ChannelHandler {
	return &handler{handlers.NewBaseHandler(courier.ChannelType("SL"), "Slack")}
}

func (h *handler) Initialize(s courier.Server) error {
	h.SetServer(s)
	s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveEvent)
	return nil
}

func handleURLVerification(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request, payload *moPayload) ([]courier.Event, error) {
	validationToken := channel.ConfigForKey(configValidationToken, "")
	if validationToken != payload.Token {
		w.WriteHeader(http.StatusForbidden)
		return nil, fmt.Errorf("Wrong validation token for channel: %s", channel.UUID())
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(payload.Challenge))
	return nil, nil
}

func (h *handler) receiveEvent(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	payload := &moPayload{}
	var payloadI PayloadInteractive
	var jsonStr string

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	if strings.HasPrefix(string(body), "payload") {
		jsonStr, err = url.QueryUnescape(string(body)[8:])
		if err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
		}

		if err := json.Unmarshal([]byte(jsonStr), &payloadI); err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
		}
	} else {
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
		}
	}

	if payloadI.Actions != nil && payload.Event.BotID == "" {
		ts := strings.Split(payloadI.Actions[0].ActionTs, ".")
		i, err := strconv.ParseInt(ts[0], 10, 64)
		if err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
		}
		date := time.Unix(int64(i), 0)

		var userName string
		var path string

		if payloadI.Channel.Name != "directmessage" { //if is a message from a slack channel that bot is in
			path = payloadI.Channel.ID
		} else { // if is a direct message from a user
			path = payloadI.User.ID
			userInfo, log, err := getUserInfo(path, channel)
			if err != nil {
				h.Backend().WriteChannelLogs(ctx, []*courier.ChannelLog{log})
				return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
			}
			userName = userInfo.User.RealName
		}

		urn, err := urns.NewURNFromParts(urns.SlackScheme, path, "", "")
		if err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
		}
		text := payloadI.Actions[0].Text.Text
		msg := h.Backend().NewIncomingMsg(channel, urn, text).WithContactName(userName).WithReceivedOn(date)
		return handlers.WriteMsgsAndResponse(ctx, h, []courier.Msg{msg}, w, r)
	}

	if payload.Type == "url_verification" {
		return handleURLVerification(ctx, channel, w, r, payload)
	}

	// if event is not a message or is from the bot ignore it
	if strings.Contains(payload.Event.Type, "message") && payload.Event.BotID == "" {

		date := time.Unix(int64(payload.EventTime), 0)

		var userName string
		var path string
		if payload.Event.ChannelType == "channel" { //if is a message from a slack channel that bot is in
			path = payload.Event.Channel
		} else if payload.Event.ChannelType == "im" { // if is a direct message from a user
			path = payload.Event.User
			userInfo, log, err := getUserInfo(payload.Event.User, channel)
			if err != nil {
				h.Backend().WriteChannelLogs(ctx, []*courier.ChannelLog{log})
				return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
			}
			userName = userInfo.User.RealName
		}

		urn, err := urns.NewURNFromParts(urns.SlackScheme, path, "", userName)
		if err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
		}

		attachmentURLs := make([]string, 0)
		for _, file := range payload.Event.Files {
			fileURL := file.URLPrivateDownload
			if fileURL == "" {
				courier.LogRequestError(r, channel, err)
			} else {
				attachmentURLs = append(attachmentURLs, fileURL)
			}
		}

		text := payload.Event.Text
		msg := h.Backend().NewIncomingMsg(channel, urn, text).WithReceivedOn(date).WithExternalID(payload.EventID).WithContactName(userName)

		for _, attURL := range attachmentURLs {
			msg.WithAttachment(attURL)
		}

		return handlers.WriteMsgsAndResponse(ctx, h, []courier.Msg{msg}, w, r)
	}
	return nil, handlers.WriteAndLogRequestIgnored(ctx, h, channel, w, r, "Ignoring request, no message")
}

func (h *handler) SendMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	botToken := msg.Channel().StringConfigForKey(configBotToken, "")
	if botToken == "" {
		return nil, fmt.Errorf("missing bot token for SL/slack channel")
	}

	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgErrored)

	hasError := true

	for _, attachment := range msg.Attachments() {
		fileAttachment, log, err := parseAttachmentToFileParams(msg, attachment)
		hasError = err != nil
		status.AddLog(log)

		if fileAttachment != nil {
			log, err = sendFilePart(msg, botToken, fileAttachment)
			hasError = err != nil
			status.AddLog(log)
		}
	}

	if len(msg.QuickReplies()) != 0 {
		log, err := sendQuickReplies(msg, botToken)
		hasError = err != nil
		status.AddLog(log)
	}

	if msg.Text() != "" && len(msg.QuickReplies()) == 0 {
		log, err := sendTextMsgPart(msg, botToken)
		hasError = err != nil
		status.AddLog(log)
	}

	if !hasError {
		status.SetStatus(courier.MsgWired)
	}

	return status, nil
}

func sendTextMsgPart(msg courier.Msg, token string) (*courier.ChannelLog, error) {
	sendURL := apiURL + "/chat.postMessage"

	msgPayload := &mtPayload{
		Channel: msg.URN().Path(),
		Text:    msg.Text(),
	}

	body, err := json.Marshal(msgPayload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, sendURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	rr, err := utils.MakeHTTPRequest(req)

	log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), rr).WithError("Message Send Error", err)

	ok, err := jsonparser.GetBoolean([]byte(rr.Body), "ok")
	if err != nil {
		return log, err
	}

	if !ok {
		errDescription, err := jsonparser.GetString([]byte(rr.Body), "error")
		if err != nil {
			return log, err
		}
		return log, errors.New(errDescription)
	}
	return log, nil
}

func parseAttachmentToFileParams(msg courier.Msg, attachment string) (*FileParams, *courier.ChannelLog, error) {
	_, attURL := handlers.SplitAttachment(attachment)

	req, err := http.NewRequest(http.MethodGet, attURL, nil)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "error building file request")
	}
	resp, err := utils.MakeHTTPRequest(req)
	log := courier.NewChannelLogFromRR("Fetching attachment", msg.Channel(), msg.ID(), resp).WithError("error fetching media", err)

	filename, err := utils.BasePathForURL(attURL)
	if err != nil {
		return nil, log, err
	}
	return &FileParams{
		File:     resp.Body,
		FileName: filename,
		Channels: msg.URN().Path(),
	}, log, nil
}

func sendFilePart(msg courier.Msg, token string, fileParams *FileParams) (*courier.ChannelLog, error) {
	uploadURL := apiURL + "/files.upload"

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	mediaPart, err := writer.CreateFormFile("file", fileParams.FileName)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create file form field")
	}
	io.Copy(mediaPart, bytes.NewReader(fileParams.File))

	filenamePart, err := writer.CreateFormField("filename")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create filename form field")
	}
	io.Copy(filenamePart, strings.NewReader(fileParams.FileName))

	channelsPart, err := writer.CreateFormField("channels")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create channels form field")
	}
	io.Copy(channelsPart, strings.NewReader(fileParams.Channels))

	writer.Close()

	req, err := http.NewRequest(http.MethodPost, uploadURL, bytes.NewReader(body.Bytes()))
	if err != nil {
		return nil, errors.Wrapf(err, "error building request to file upload endpoint")
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Add("Content-Type", writer.FormDataContentType())
	resp, err := utils.MakeHTTPRequest(req)
	if err != nil {
		return nil, errors.Wrapf(err, "error uploading file to slack")
	}

	var fr FileResponse
	if err := json.Unmarshal([]byte(resp.Body), &fr); err != nil {
		return nil, errors.Errorf("couldn't unmarshal file response: %v", err)
	}

	if !fr.OK {
		return nil, errors.Errorf("error uploading file to slack: %s.", fr.Error)
	}

	return courier.NewChannelLogFromRR("uploading file to Slack", msg.Channel(), msg.ID(), resp).WithError("Error uploading file to Slack", err), nil
}

func sendQuickReplies(msg courier.Msg, botToken string) (*courier.ChannelLog, error) {
	sendURL := apiURL + "/chat.postMessage"

	payload := &mtPayload{
		Channel: msg.URN().Path(),
		Blocks: []Block{
			{
				Type: "section",
				Text: &Text{
					Type:  "plain_text",
					Text:  msg.Text(),
					Emoji: true,
				},
			},
		},
	}

	bl := Block{
		Type: "actions",
	}
	payload.Blocks = append(payload.Blocks, bl)

	for _, qr := range msg.QuickReplies() {

		bt := Button{
			Type: "button",
			Text: Text{
				Type:  "plain_text",
				Emoji: true,
				Text:  qr,
			},
			Value: qr,
		}
		payload.Blocks[1].Elements = append(payload.Blocks[1].Elements, bt)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, sendURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", botToken))

	rr, err := utils.MakeHTTPRequest(req)
	log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), rr).WithError("Message Send Error", err)
	if err != nil {
		return log, err
	}

	ok, err := jsonparser.GetBoolean([]byte(rr.Body), "ok")
	if err != nil {
		return log, err
	}

	if !ok {
		_, err := jsonparser.GetString([]byte(rr.Body), "error")
		if err != nil {
			return log, err
		}
	}
	return log, nil
}

func getUserInfo(userSlackID string, channel courier.Channel) (*UserInfo, *courier.ChannelLog, error) {
	resource := "/users.info"
	urlStr := apiURL + resource

	req, err := http.NewRequest(http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, nil, err
	}

	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Add("Authorization", "Bearer "+channel.StringConfigForKey(configBotToken, ""))

	q := req.URL.Query()
	q.Add("user", userSlackID)
	req.URL.RawQuery = q.Encode()

	rr, err := utils.MakeHTTPRequest(req)
	if err != nil {
		log := courier.NewChannelLogFromRR("Get User info", channel, courier.NilMsgID, rr).WithError("Request User Info Error", err)
		return nil, log, err
	}

	var uInfo *UserInfo
	if err := json.Unmarshal(rr.Body, &uInfo); err != nil {
		log := courier.NewChannelLogFromRR("Get User info", channel, courier.NilMsgID, rr).WithError("Unmarshal User Info Error", err)
		return nil, log, err
	}

	return uInfo, nil, nil
}

// mtPayload is a struct that represents the body of a SendMmsg text part
type mtPayload struct {
	Channel string  `json:"channel"`
	Text    string  `json:"text,omitempty"`
	Blocks  []Block `json:"blocks,omitempty"`
}

type Block struct {
	Type     string   `json:"type,omitempty"`
	Text     *Text    `json:"text,omitempty"`
	Elements []Button `json:"elements,omitempty"`
}

type Text struct {
	Type  string `json:"type,omitempty"`
	Text  string `json:"text,omitempty"` //length: 75 characters
	Emoji bool   `json:"emoji,omitempty"`
}

type Button struct {
	Type  string `json:"type"`
	Text  Text   `json:"text,omitempty"`
	URL   string `json:"url,omitempty"`
	Value string `json:"value"`
}

// moPayload is a struct that represents message payload from message type event
type moPayload struct {
	Token    string `json:"token,omitempty"`
	TeamID   string `json:"team_id,omitempty"`
	APIAppID string `json:"api_app_id,omitempty"`
	Event    struct {
		Type        string `json:"type,omitempty"`
		Channel     string `json:"channel,omitempty"`
		User        string `json:"user,omitempty"`
		Text        string `json:"text,omitempty"`
		Ts          string `json:"ts,omitempty"`
		EventTs     string `json:"event_ts,omitempty"`
		ChannelType string `json:"channel_type,omitempty"`
		Files       []File `json:"files"`
		BotID       string `json:"bot_id,omitempty"`
	} `json:"event,omitempty"`
	Type           string   `json:"type,omitempty"`
	AuthedUsers    []string `json:"authed_users,omitempty"`
	AuthedTeams    []string `json:"authed_teams,omitempty"`
	Authorizations []struct {
		EnterpriseID string `json:"enterprise_id,omitempty"`
		TeamID       string `json:"team_id,omitempty"`
		UserID       string `json:"user_id,omitempty"`
		IsBot        bool   `json:"is_bot,omitempty"`
	} `json:"authorizations,omitempty"`
	EventContext string `json:"event_context,omitempty"`
	EventID      string `json:"event_id,omitempty"`
	EventTime    int    `json:"event_time,omitempty"`
	Challenge    string `json:"challenge,omitempty"`
}

// File is a struct that represents file item that can be present in Files list in message event, or in FileResponse or in FileParams
type File struct {
	ID                 string `json:"id"`
	Created            int    `json:"created"`
	Timestamp          int    `json:"timestamp"`
	Name               string `json:"name"`
	Title              string `json:"title"`
	Mimetype           string `json:"mimetype"`
	Filetype           string `json:"filetype"`
	PrettyType         string `json:"pretty_type"`
	User               string `json:"user"`
	Editable           bool   `json:"editable"`
	Size               int    `json:"size"`
	Mode               string `json:"mode"`
	IsExternal         bool   `json:"is_external"`
	ExternalType       string `json:"external_type"`
	IsPublic           bool   `json:"is_public"`
	PublicURLShared    bool   `json:"public_url_shared"`
	DisplayAsBot       bool   `json:"display_as_bot"`
	Username           string `json:"username"`
	URLPrivate         string `json:"url_private"`
	URLPrivateDownload string `json:"url_private_download"`
	MediaDisplayType   string `json:"media_display_type"`
	Thumb64            string `json:"thumb_64"`
	Thumb80            string `json:"thumb_80"`
	Thumb360           string `json:"thumb_360"`
	Thumb360W          int    `json:"thumb_360_w"`
	Thumb360H          int    `json:"thumb_360_h"`
	Thumb160           string `json:"thumb_160"`
	OriginalW          int    `json:"original_w"`
	OriginalH          int    `json:"original_h"`
	ThumbTiny          string `json:"thumb_tiny"`
	Permalink          string `json:"permalink"`
	PermalinkPublic    string `json:"permalink_public"`
	HasRichPreview     bool   `json:"has_rich_preview"`
}

// FileResponse is a struct that represents the response from a request in files.sharedPublicURL to make public and shareable a file that is sent in a message, more information see https://api.slack.com/methods/files.sharedPublicURL.
type FileResponse struct {
	OK    bool   `json:"ok"`
	File  File   `json:"file"`
	Error string `json:"error"`
}

// FileParams is a struct that represents the request params send to slack api files.upload method to send a file to a channel conversation or a direct message conversation with a user, more
// information see https://api.slack.com/methods/files.upload.
type FileParams struct {
	File     []byte `json:"file,omitempty"`
	FileName string `json:"filename,omitempty"`
	Channels string `json:"channels,omitempty"`
}

// UserInfo is a struct that represents the response from request in users.info slack api method, more information see https://api.slack.com/methods/users.info.
type UserInfo struct {
	Ok   bool `json:"ok"`
	User struct {
		ID       string `json:"id"`
		TeamID   string `json:"team_id"`
		Name     string `json:"name"`
		Deleted  bool   `json:"deleted"`
		Color    string `json:"color"`
		RealName string `json:"real_name"`
		Tz       string `json:"tz"`
		TzLabel  string `json:"tz_label"`
		TzOffset int    `json:"tz_offset"`
		Profile  struct {
			AvatarHash            string `json:"avatar_hash"`
			StatusText            string `json:"status_text"`
			StatusEmoji           string `json:"status_emoji"`
			RealName              string `json:"real_name"`
			DisplayName           string `json:"display_name"`
			RealNameNormalized    string `json:"real_name_normalized"`
			DisplayNameNormalized string `json:"display_name_normalized"`
			Email                 string `json:"email"`
			ImageOriginal         string `json:"image_original"`
			Image24               string `json:"image_24"`
			Image32               string `json:"image_32"`
			Image48               string `json:"image_48"`
			Image72               string `json:"image_72"`
			Image192              string `json:"image_192"`
			Image512              string `json:"image_512"`
			Team                  string `json:"team"`
		} `json:"profile"`
	} `json:"user"`
}

type PayloadInteractive struct {
	Type string `json:"type"`
	User struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Name     string `json:"name"`
		TeamID   string `json:"team_id"`
	} `json:"user"`
	APIAppID  string `json:"api_app_id"`
	Token     string `json:"token"`
	Container struct {
		Type        string `json:"type"`
		MessageTs   string `json:"message_ts"`
		ChannelID   string `json:"channel_id"`
		IsEphemeral bool   `json:"is_ephemeral"`
	} `json:"container"`
	TriggerID string `json:"trigger_id"`
	Team      struct {
		ID     string `json:"id"`
		Domain string `json:"domain"`
	} `json:"team"`
	Enterprise          interface{} `json:"enterprise"`
	IsEnterpriseInstall bool        `json:"is_enterprise_install"`
	Channel             struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"channel"`
	Message struct {
		BotID  string `json:"bot_id"`
		Type   string `json:"type"`
		Text   string `json:"text"`
		User   string `json:"user"`
		Ts     string `json:"ts"`
		AppID  string `json:"app_id"`
		Team   string `json:"team"`
		Blocks []struct {
			Type     string `json:"type"`
			BlockID  string `json:"block_id"`
			ImageURL string `json:"image_url,omitempty"`
			AltText  string `json:"alt_text,omitempty"`
			Title    struct {
				Type  string `json:"type"`
				Text  string `json:"text"`
				Emoji bool   `json:"emoji"`
			} `json:"title,omitempty"`
			ImageWidth  int    `json:"image_width,omitempty"`
			ImageHeight int    `json:"image_height,omitempty"`
			ImageBytes  int    `json:"image_bytes,omitempty"`
			IsAnimated  bool   `json:"is_animated,omitempty"`
			Fallback    string `json:"fallback,omitempty"`
			Text        struct {
				Type  string `json:"type"`
				Text  string `json:"text"`
				Emoji bool   `json:"emoji"`
			} `json:"text,omitempty"`
			Elements []struct {
				Type     string `json:"type"`
				ActionID string `json:"action_id"`
				Text     struct {
					Type  string `json:"type"`
					Text  string `json:"text"`
					Emoji bool   `json:"emoji"`
				} `json:"text"`
				Value string `json:"value"`
			} `json:"elements,omitempty"`
		} `json:"blocks"`
	} `json:"message"`
	State struct {
		Values struct {
		} `json:"values"`
	} `json:"state"`
	ResponseURL string `json:"response_url"`
	Actions     []struct {
		ActionID string `json:"action_id"`
		BlockID  string `json:"block_id"`
		Text     struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Emoji bool   `json:"emoji"`
		} `json:"text"`
		Value    string `json:"value"`
		Type     string `json:"type"`
		ActionTs string `json:"action_ts"`
	} `json:"actions"`
}
