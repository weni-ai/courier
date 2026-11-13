package facebookapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/gocommon/urns"
	"github.com/sirupsen/logrus"
)

var wacStandbyPlaceholder = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

const (
	wacStandbyOriginShopper = "shopper"
	wacStandbyOriginOther   = "outro atendimento"
	wacStandbySeenKeyPrefix = "wac:standby:seen"
	wacStandbySeenTTL       = 24 * time.Hour
	wacStandbyFlowSeparator = " — "
	wacStandbyValueJoin     = " | "
)

type wacStandbyBody struct {
	Contacts      []wacHandoverContact `json:"contacts"`
	Messages      []wacStandbyMessage  `json:"messages"`
	MessageEchoes []wacStandbyEcho     `json:"message_echoes"`
	Statuses      []wacStandbyStatus   `json:"statuses"`
}

type wacStandbyMessage struct {
	ID         string `json:"id"`
	From       string `json:"from"`
	FromUserID string `json:"from_user_id"`
	Timestamp  string `json:"timestamp"`
	Type       string `json:"type"`
	Text       struct {
		Body string `json:"body"`
	} `json:"text"`
	Image       *wacMedia             `json:"image"`
	Audio       *wacMedia             `json:"audio"`
	Video       *wacMedia             `json:"video"`
	Document    *wacMedia             `json:"document"`
	Voice       *wacMedia             `json:"voice"`
	Sticker     *wacSticker           `json:"sticker"`
	Button      *wacStandbyButton     `json:"button"`
	Interactive wacStandbyInteractive `json:"interactive"`
}

type wacStandbyButton struct {
	Text string `json:"text"`
}

type wacStandbyInteractive struct {
	Type   string `json:"type"`
	Header struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"header"`
	Body struct {
		Text string `json:"text"`
	} `json:"body"`
	Footer struct {
		Text string `json:"text"`
	} `json:"footer"`
	Action struct {
		Name       string `json:"name"`
		Parameters struct {
			FlowCTA string `json:"flow_cta"`
		} `json:"parameters"`
	} `json:"action"`
	ButtonReply struct {
		Title string `json:"title"`
	} `json:"button_reply"`
	ListReply struct {
		Title string `json:"title"`
	} `json:"list_reply"`
}

type wacStandbyEcho struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		To        string `json:"to"`
		Recipient string `json:"recipient"`
		Type      string `json:"type"`
		Text      struct {
			Body string `json:"body"`
		} `json:"text"`
		Template    *wacStandbySendTemplate `json:"template"`
		Interactive wacStandbyInteractive   `json:"interactive"`
		Button      *wacStandbyButton       `json:"button"`
	} `json:"message"`
	Template *wacStandbyTemplateDef `json:"template"`
	Flow     *wacStandbyFlow        `json:"flow"`
}

type wacStandbyFlow struct {
	Name string `json:"name"`
}

type wacStandbyStatus struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	Timestamp       string `json:"timestamp"`
	RecipientID     string `json:"recipient_id"`
	RecipientUserID string `json:"recipient_user_id"`
}

type wacStandbyParameter struct {
	Type          string `json:"type"`
	Text          string `json:"text"`
	ParameterName string `json:"parameter_name"`
}

type wacStandbySendComponent struct {
	Type       string                `json:"type"`
	SubType    string                `json:"sub_type"`
	Index      int                   `json:"index"`
	Parameters []wacStandbyParameter `json:"parameters"`
}

type wacStandbySendTemplate struct {
	Name       string                    `json:"name"`
	Components []wacStandbySendComponent `json:"components"`
}

type wacStandbyTemplateButton struct {
	Type string `json:"type"`
	Text string `json:"text"`
	URL  string `json:"url"`
}

type wacStandbyTemplateComponent struct {
	Type    string                     `json:"type"`
	Format  string                     `json:"format"`
	Text    string                     `json:"text"`
	Buttons []wacStandbyTemplateButton `json:"buttons"`
}

type wacStandbyTemplateDef struct {
	Name       string                        `json:"name"`
	Language   json.RawMessage               `json:"language"`
	Components []wacStandbyTemplateComponent `json:"components"`
}

type wacStandbyView struct {
	Type        string
	Text        string
	ButtonText  string
	Interactive wacStandbyInteractive
	Send        *wacStandbySendTemplate
	Structure   *wacStandbyTemplateDef
	FlowName    string
	HasFlow     bool
}

func renderWACStandbyShopper(msg wacStandbyMessage) (string, bool) {
	view := wacStandbyView{
		Type:        msg.Type,
		Text:        msg.Text.Body,
		Interactive: msg.Interactive,
	}
	if msg.Button != nil {
		view.ButtonText = msg.Button.Text
	}
	return renderWACStandbyView(wacStandbyOriginShopper, view)
}

func renderWACStandbyEcho(echo wacStandbyEcho) (string, bool) {
	view := wacStandbyView{
		Type:        echo.Message.Type,
		Text:        echo.Message.Text.Body,
		Interactive: echo.Message.Interactive,
		Send:        echo.Message.Template,
		Structure:   echo.Template,
	}
	if echo.Message.Button != nil {
		view.ButtonText = echo.Message.Button.Text
	}
	if echo.Flow != nil {
		view.HasFlow = true
		view.FlowName = echo.Flow.Name
	}
	return renderWACStandbyView(wacStandbyOriginOther, view)
}

func renderWACStandbyView(origin string, view wacStandbyView) (string, bool) {
	kind := strings.ToLower(strings.TrimSpace(view.Type))
	switch kind {
	case "text":
		body := strings.TrimSpace(view.Text)
		if body == "" {
			return "", false
		}
		return standbyWithOrigin(origin, []string{body})
	case "image", "audio", "video", "document", "voice", "sticker":
		return standbyWithOrigin(origin, []string{"<" + kind + ">"})
	case "template":
		if view.Structure != nil {
			return renderWACStandbyStructuredTemplate(origin, view.Structure, view.Send)
		}
		return renderWACStandbyBareTemplate(origin, view.Send)
	case "interactive":
		if strings.EqualFold(view.Interactive.Type, "flow") || view.HasFlow {
			return renderWACStandbyFlow(origin, view)
		}
		title := strings.TrimSpace(view.Interactive.ButtonReply.Title)
		if title == "" {
			title = strings.TrimSpace(view.Interactive.ListReply.Title)
		}
		if title != "" {
			return standbyWithOrigin(origin, []string{title})
		}
		return standbyWithOrigin(origin, []string{"<interactive>"})
	case "button":
		if text := strings.TrimSpace(view.ButtonText); text != "" {
			return standbyWithOrigin(origin, []string{text})
		}
		return standbyWithOrigin(origin, []string{"<button>"})
	default:
		if view.HasFlow || strings.EqualFold(view.Interactive.Type, "flow") {
			return renderWACStandbyFlow(origin, view)
		}
		if text := strings.TrimSpace(view.ButtonText); text != "" {
			return standbyWithOrigin(origin, []string{text})
		}
		title := strings.TrimSpace(view.Interactive.ButtonReply.Title)
		if title == "" {
			title = strings.TrimSpace(view.Interactive.ListReply.Title)
		}
		if title != "" {
			return standbyWithOrigin(origin, []string{title})
		}
		if kind == "" {
			return "", false
		}
		return standbyWithOrigin(origin, []string{"<" + kind + ">"})
	}
}

func renderWACStandbyFlow(origin string, view wacStandbyView) (string, bool) {
	parts := standbyNonEmpty(
		view.Interactive.Header.Text,
		view.Interactive.Body.Text,
		view.Interactive.Footer.Text,
		view.Interactive.Action.Parameters.FlowCTA,
	)
	var line string
	switch {
	case len(parts) > 0:
		line = strings.Join(parts, wacStandbyFlowSeparator)
	case strings.TrimSpace(view.FlowName) != "":
		line = strings.TrimSpace(view.FlowName)
	default:
		line = "<flow>"
	}
	return standbyWithOrigin(origin, []string{line})
}

func renderWACStandbyStructuredTemplate(origin string, def *wacStandbyTemplateDef, send *wacStandbySendTemplate) (string, bool) {
	if def == nil {
		return "", false
	}
	lines := make([]string, 0, len(def.Components))
	buttonIndex := 0
	for _, component := range def.Components {
		switch strings.ToUpper(strings.TrimSpace(component.Type)) {
		case "HEADER":
			format := strings.ToUpper(strings.TrimSpace(component.Format))
			switch format {
			case "IMAGE", "VIDEO", "DOCUMENT", "AUDIO":
				lines = append(lines, "<"+strings.ToLower(format)+">")
			default:
				if text := strings.TrimSpace(standbySubstitute(component.Text, standbyParams(send, "header"))); text != "" {
					lines = append(lines, text)
				}
			}
		case "BODY":
			if text := strings.TrimSpace(standbySubstitute(component.Text, standbyParams(send, "body"))); text != "" {
				lines = append(lines, text)
			}
		case "FOOTER":
			if text := strings.TrimSpace(component.Text); text != "" {
				lines = append(lines, text)
			}
		case "BUTTONS":
			for _, button := range component.Buttons {
				label := strings.TrimSpace(button.Text)
				url := strings.TrimSpace(standbySubstitute(button.URL, standbyButtonParams(send, buttonIndex)))
				buttonIndex++
				switch {
				case label != "" && url != "":
					lines = append(lines, label+": "+url)
				case label != "":
					lines = append(lines, label)
				case url != "":
					lines = append(lines, url)
				}
			}
		}
	}
	return standbyWithOrigin(origin, lines)
}

func renderWACStandbyBareTemplate(origin string, send *wacStandbySendTemplate) (string, bool) {
	if send == nil || strings.TrimSpace(send.Name) == "" {
		return "", false
	}
	values := make([]string, 0)
	for _, component := range send.Components {
		for _, param := range component.Parameters {
			switch strings.ToLower(strings.TrimSpace(param.Type)) {
			case "text":
				if text := strings.TrimSpace(param.Text); text != "" {
					values = append(values, text)
				}
			case "image", "video", "document", "audio":
				values = append(values, "<"+strings.ToLower(strings.TrimSpace(param.Type))+">")
			}
		}
	}
	line := "template:" + strings.TrimSpace(send.Name)
	if len(values) > 0 {
		line += " " + strings.Join(values, wacStandbyValueJoin)
	}
	return standbyWithOrigin(origin, []string{line})
}

func standbyParams(send *wacStandbySendTemplate, componentType string) []wacStandbyParameter {
	if send == nil {
		return nil
	}
	for _, component := range send.Components {
		if strings.EqualFold(component.Type, componentType) {
			return component.Parameters
		}
	}
	return nil
}

func standbyButtonParams(send *wacStandbySendTemplate, index int) []wacStandbyParameter {
	if send == nil {
		return nil
	}
	for _, component := range send.Components {
		if strings.EqualFold(component.Type, "button") && component.Index == index {
			return component.Parameters
		}
	}
	return nil
}

func standbySubstitute(text string, params []wacStandbyParameter) string {
	if text == "" {
		return ""
	}
	textParams := make([]string, 0, len(params))
	named := make(map[string]string)
	for _, param := range params {
		if !strings.EqualFold(param.Type, "text") && param.Type != "" {
			continue
		}
		if param.Type == "" && param.Text == "" && param.ParameterName == "" {
			continue
		}
		textParams = append(textParams, param.Text)
		if param.ParameterName != "" {
			named[param.ParameterName] = param.Text
		}
	}
	return wacStandbyPlaceholder.ReplaceAllStringFunc(text, func(match string) string {
		inner := match[2 : len(match)-2]
		if n, err := strconv.Atoi(inner); err == nil {
			if n >= 1 && n <= len(textParams) {
				return textParams[n-1]
			}
			return match
		}
		if value, ok := named[inner]; ok {
			return value
		}
		return match
	})
}

func standbyWithOrigin(origin string, lines []string) (string, bool) {
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			kept = append(kept, line)
		}
	}
	if len(kept) == 0 {
		return "", false
	}
	kept[0] = "[" + origin + "] " + kept[0]
	return strings.Join(kept, "\n"), true
}

func (h *handler) processWACStandby(
	ctx context.Context,
	channel courier.Channel,
	raw json.RawMessage,
	legacy wacHandoverValue,
	entryTime int64,
	w http.ResponseWriter,
	r *http.Request,
) ([]string, error) {
	var body wacStandbyBody
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
	}

	wrote := false
	statusApplied := false
	for _, msg := range body.Messages {
		appended, err := h.appendWACStandbyLine(ctx, channel, body.Contacts, msg.From, msg.FromUserID, msg.ID, msg.Timestamp, entryTime, func() (string, bool) {
			return renderWACStandbyShopper(msg)
		})
		if err != nil {
			return nil, err
		}
		wrote = wrote || appended
	}
	for _, echo := range body.MessageEchoes {
		appended, err := h.appendWACStandbyLine(ctx, channel, body.Contacts, echo.Message.To, echo.Message.Recipient, echo.ID, echo.Timestamp, entryTime, func() (string, bool) {
			return renderWACStandbyEcho(echo)
		})
		if err != nil {
			return nil, err
		}
		wrote = wrote || appended
	}
	for _, status := range body.Statuses {
		applied, err := h.applyWACStandbyStatus(channel, status, entryTime)
		if err != nil {
			return nil, err
		}
		statusApplied = statusApplied || applied
	}
	if err := h.applyLegacyStandbyOwner(channel, legacy, entryTime); err != nil {
		return nil, err
	}

	infos := make([]string, 0, 3)
	if wrote {
		infos = append(infos, "standby context recorded")
	}
	if statusApplied {
		infos = append(infos, "standby status recorded")
	}

	legacyInfo, err := h.persistWAHandoverContext(ctx, channel, legacy, entryTime, w, r)
	if err != nil {
		return nil, err
	}
	if legacyInfo == "wa conversation handover persisted" {
		infos = append(infos, legacyInfo)
	}
	if len(infos) == 0 {
		infos = append(infos, "standby without conversation context")
	}
	return infos, nil
}

func (h *handler) appendWACStandbyLine(
	ctx context.Context,
	channel courier.Channel,
	contacts []wacHandoverContact,
	primaryURN string,
	fallbackURN string,
	messageID string,
	timestamp string,
	entryTime int64,
	render func() (string, bool),
) (bool, error) {
	urn, ok := standbyContactURN(primaryURN, fallbackURN)
	if !ok {
		logWACStandbySkipped(channel, messageID, "no_urn")
		return false, nil
	}
	occurred, ts, callLua := standbyOccurredOn(timestamp, entryTime)
	if err := h.applyStandbyNotOwner(channel, urn, messageID, ts, callLua); err != nil {
		return false, err
	}
	text, ok := render()
	if !ok {
		return false, nil
	}
	if messageID == "" {
		logWACStandbySkipped(channel, messageID, "no_message_id")
		return false, nil
	}
	claimed, err := h.claimWACStandbySeen(channel, messageID)
	if err != nil {
		return false, err
	}
	if !claimed {
		return false, nil
	}
	err = h.persistRenderedWAHandover(ctx, channel, urn, wacHandoverContactName(contacts, urn), courier.WAHandoverContextHistory, text, nil, occurred, nil)
	if err != nil {
		h.deleteWACStandbySeen(channel, messageID)
		logrus.WithError(err).WithFields(logrus.Fields{
			"event":        "wac_standby_context_write_failed",
			"channel_uuid": channel.UUID(),
			"external_id":  messageID,
		}).Error("standby context write failed")
		return false, err
	}
	return true, nil
}

func (h *handler) applyWACStandbyStatus(channel courier.Channel, status wacStandbyStatus, entryTime int64) (bool, error) {
	urn, ok := standbyContactURN(status.RecipientID, status.RecipientUserID)
	if !ok {
		logWACStandbySkipped(channel, status.ID, "no_urn")
		return false, nil
	}
	_, ts, callLua := standbyOccurredOn(status.Timestamp, entryTime)
	if err := h.applyStandbyNotOwner(channel, urn, status.ID, ts, callLua); err != nil {
		return false, err
	}
	return true, nil
}

func (h *handler) applyLegacyStandbyOwner(channel courier.Channel, legacy wacHandoverValue, entryTime int64) error {
	urn, err := resolveWAHandoverSenderURN(legacy.Sender)
	if err != nil {
		return nil
	}
	_, ts, callLua := standbyOccurredOn(legacy.Timestamp, entryTime)
	return h.applyStandbyNotOwner(channel, urn, "", ts, callLua)
}

func (h *handler) applyStandbyNotOwner(channel courier.Channel, urn urns.URN, externalID string, ts int64, callLua bool) error {
	if !callLua {
		logWACStandbySkipped(channel, externalID, "bad_timestamp")
		return nil
	}
	rc := h.Backend().RedisPool().Get()
	defer rc.Close()
	_, err := applyWACThreadOwner(rc, channel.UUID(), urn, ts, wacThreadOwnerStateNone, wacThreadOwnerModeSet, "standby")
	return err
}

func standbySeenKey(channelUUID courier.ChannelUUID, messageID string) string {
	return fmt.Sprintf("%s:%s:%s", wacStandbySeenKeyPrefix, channelUUID, messageID)
}

func (h *handler) claimWACStandbySeen(channel courier.Channel, messageID string) (bool, error) {
	rc := h.Backend().RedisPool().Get()
	defer rc.Close()
	_, err := redis.String(rc.Do("SET", standbySeenKey(channel.UUID(), messageID), "1", "NX", "EX", int(wacStandbySeenTTL.Seconds())))
	if err == redis.ErrNil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (h *handler) deleteWACStandbySeen(channel courier.Channel, messageID string) {
	rc := h.Backend().RedisPool().Get()
	defer rc.Close()
	_, _ = rc.Do("DEL", standbySeenKey(channel.UUID(), messageID))
}

func standbyContactURN(primary, fallback string) (urns.URN, bool) {
	id := strings.TrimSpace(primary)
	if id == "" {
		id = strings.TrimSpace(fallback)
	}
	if id == "" {
		return urns.NilURN, false
	}
	urn, err := urns.NewWhatsAppURN(id)
	if err != nil {
		return urns.NilURN, false
	}
	return urn, true
}

func standbyOccurredOn(timestamp string, entryTime int64) (time.Time, int64, bool) {
	fallback := time.Unix(entryTime, 0).UTC()
	if strings.TrimSpace(timestamp) == "" {
		return fallback, entryTime, true
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return fallback, 0, false
	}
	return time.Unix(parsed, 0).UTC(), parsed, true
}

func logWACStandbySkipped(channel courier.Channel, externalID, reason string) {
	fields := logrus.Fields{
		"event":        "wac_standby_skipped",
		"channel_uuid": channel.UUID(),
		"reason":       reason,
	}
	if externalID != "" {
		fields["external_id"] = externalID
	}
	logrus.WithFields(fields).Info("wac standby skipped")
}

func standbyNonEmpty(parts ...string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if text := strings.TrimSpace(part); text != "" {
			out = append(out, text)
		}
	}
	return out
}
