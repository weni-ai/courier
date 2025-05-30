package metrics

import (
	"os"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
)

var channelsToMonitor = map[uuid.UUID]bool{}
var monitorAllChannels = os.Getenv("COURIER_PROMETHEUS_MONITOR_ALL_CHANNELS") == "true"

func init() {
	channelsString := os.Getenv("COURIER_PROMETHEUS_MONITOR_CHANNELS")
	if channelsString != "" {
		channels := strings.Split(channelsString, ",")
		for _, channel := range channels {
			channelUUID := uuid.FromStringOrNil(channel)
			if channelUUID == uuid.Nil {
				logrus.Errorf("Invalid channel UUID %s", channel)
				continue
			}
			channelsToMonitor[channelUUID] = true
		}
	}

	logrus.WithField("orgs", channelsToMonitor).Info("prometheus orgs to monitor")
}

var summaryObjectives = map[float64]float64{
	0.5:  0.05,  // 50th percentile with a max. absolute error of 0.05.
	0.90: 0.01,  // 90th percentile with a max. absolute error of 0.01.
	0.95: 0.005, // 95th percentile with a max. absolute error of 0.005.
	0.99: 0.001, // 99th percentile with a max. absolute error of 0.001.
}

var usedWorkers = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "cr_used_workers",
	Help: "The number of workers currently in use",
})

var availableWorkers = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "cr_available_workers",
	Help: "The number of workers currently available",
})

var msg_send_error_channel_type = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cr_msg_send_error",
	Help: "The number of messages sent with an error (Errored or Failed)",
}, []string{"channel_type"})

var msg_send_success_channel_type = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cr_msg_send_success",
	Help: "The number of messages sent successfully",
}, []string{"channel_type"})

var msg_send_error_duration_channel_type = promauto.NewSummaryVec(prometheus.SummaryOpts{
	Name:       "cr_msg_send_error_duration",
	Help:       "The processing duration of messages sent with an error (Errored or Failed)",
	Objectives: summaryObjectives,
}, []string{"channel_type"})

var msg_send_success_duration_channel_type = promauto.NewSummaryVec(prometheus.SummaryOpts{
	Name:       "cr_msg_send_success_duration",
	Help:       "The processing duration of messages sent successfully",
	Objectives: summaryObjectives,
}, []string{"channel_type"})

var msg_send_success_channel_uuid = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cr_msg_send_success_channel",
	Help: "The number of messages sent successfully",
}, []string{"channel_uuid"})

var msg_send_error_channel_uuid = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cr_msg_send_error_channel",
	Help: "The number of messages sent with an error (Errored or Failed)",
}, []string{"channel_uuid"})

var msg_send_error_duration_channel_uuid = promauto.NewSummaryVec(prometheus.SummaryOpts{
	Name:       "cr_msg_send_error_duration_channel",
	Help:       "The processing duration of messages sent with an error (Errored or Failed)",
	Objectives: summaryObjectives,
}, []string{"channel_uuid"})

var msg_send_success_duration_channel_uuid = promauto.NewSummaryVec(prometheus.SummaryOpts{
	Name:       "cr_msg_send_success_duration_channel",
	Help:       "The processing duration of messages sent successfully",
	Objectives: summaryObjectives,
}, []string{"channel_uuid"})

var channel_error_by_type = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cr_channel_error_by_type",
	Help: "The number of errors for a channel",
}, []string{"channel_type"})

var channel_ignored_by_type = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cr_channel_ignored_by_type",
	Help: "The number of ignored requests for a channel",
}, []string{"channel_type"})

var channel_error_by_uuid = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cr_channel_error_by_uuid",
	Help: "The number of errors for a channel",
}, []string{"channel_uuid"})

var channel_ignored_by_uuid = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cr_channel_ignored_by_uuid",
	Help: "The number of ignored requests for a channel",
}, []string{"channel_uuid"})

var msg_receive_by_type = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cr_msg_receive_by_type",
	Help: "The number of messages received for a channel",
}, []string{"channel_type"})

var msg_receive_by_uuid = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cr_msg_receive_by_uuid",
	Help: "The number of messages received for a channel",
}, []string{"channel_uuid"})

var channel_event_receive_by_type = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cr_channel_event_receive_by_type",
	Help: "The number of channel events received for a channel",
}, []string{"channel_type"})

var channel_event_receive_by_uuid = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cr_channel_event_receive_by_uuid",
	Help: "The number of channel events received for a channel",
}, []string{"channel_uuid"})

var msg_status_receive_by_type = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cr_msg_status_receive_by_type",
	Help: "The number of message statuses received for a channel",
}, []string{"channel_type"})

var msg_status_receive_by_uuid = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cr_msg_status_receive_by_uuid",
	Help: "The number of message statuses received for a channel",
}, []string{"channel_uuid"})

var bulk_queue_size = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "cr_bulk_queue_size",
	Help: "The size of the bulk queue (redis queue size)",
})

var priority_queue_size = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "cr_priority_queue_size",
	Help: "The size of the priority queue (redis queue size)",
})

var new_contacts_count = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "cr_new_contacts_count",
	Help: "The number of new contacts",
})

var new_contacts_by_type = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cr_new_contacts_by_type",
	Help: "The number of new contacts by type",
}, []string{"channel_type"})

var new_contacts_by_uuid = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "cr_new_contacts_by_uuid",
	Help: "The number of new contacts by uuid",
}, []string{"channel_uuid"})

func SetAvailableWorkers(count int) {
	availableWorkers.Set(float64(count))
}

func SetUsedWorkers(count int) {
	usedWorkers.Set(float64(count))
}

func SetMsgSendErrorByType(channelType string, duration float64) {
	msg_send_error_channel_type.WithLabelValues(channelType).Set(duration)
}

func SetMsgSendSuccessByType(channelType string, duration float64) {
	msg_send_success_channel_type.WithLabelValues(channelType).Set(duration)
}

func SetMsgSendErrorDurationByType(channelType string, duration float64) {
	msg_send_error_duration_channel_type.WithLabelValues(channelType).Observe(duration)
}

func SetMsgSendSuccessDurationByType(channelType string, duration float64) {
	msg_send_success_duration_channel_type.WithLabelValues(channelType).Observe(duration)
}

func SetMsgSendErrorByUUID(channelUUID uuid.UUID, duration float64) {
	if monitorAllChannels || channelsToMonitor[channelUUID] {
		msg_send_error_channel_uuid.WithLabelValues(channelUUID.String()).Set(duration)
	}
}

func SetMsgSendSuccessByUUID(channelUUID uuid.UUID, duration float64) {
	if monitorAllChannels || channelsToMonitor[channelUUID] {
		msg_send_success_channel_uuid.WithLabelValues(channelUUID.String()).Set(duration)
	}
}

func SetMsgSendErrorDurationByUUID(channelUUID uuid.UUID, duration float64) {
	if monitorAllChannels || channelsToMonitor[channelUUID] {
		msg_send_error_duration_channel_uuid.WithLabelValues(channelUUID.String()).Observe(duration)
	}
}

func SetMsgSendSuccessDurationByUUID(channelUUID uuid.UUID, duration float64) {
	if monitorAllChannels || channelsToMonitor[channelUUID] {
		msg_send_success_duration_channel_uuid.WithLabelValues(channelUUID.String()).Observe(duration)
	}
}

func SetChannelErrorByType(channelType string, duration float64) {
	channel_error_by_type.WithLabelValues(channelType).Set(duration)
}

func SetChannelIgnoredByType(channelType string, duration float64) {
	channel_ignored_by_type.WithLabelValues(channelType).Set(duration)
}

func SetChannelErrorByUUID(channelUUID uuid.UUID, duration float64) {
	if monitorAllChannels || channelsToMonitor[channelUUID] {
		channel_error_by_uuid.WithLabelValues(channelUUID.String()).Set(duration)
	}
}

func SetChannelIgnoredByUUID(channelUUID uuid.UUID, duration float64) {
	if monitorAllChannels || channelsToMonitor[channelUUID] {
		channel_ignored_by_uuid.WithLabelValues(channelUUID.String()).Set(duration)
	}
}

func SetMsgReceiveByType(channelType string, duration float64) {
	msg_receive_by_type.WithLabelValues(channelType).Set(duration)
}

func SetMsgReceiveByUUID(channelUUID uuid.UUID, duration float64) {
	if monitorAllChannels || channelsToMonitor[channelUUID] {
		msg_receive_by_uuid.WithLabelValues(channelUUID.String()).Set(duration)
	}
}

func SetChannelEventReceiveByType(channelType string, duration float64) {
	channel_event_receive_by_type.WithLabelValues(channelType).Set(duration)
}

func SetChannelEventReceiveByUUID(channelUUID uuid.UUID, duration float64) {
	if monitorAllChannels || channelsToMonitor[channelUUID] {
		channel_event_receive_by_uuid.WithLabelValues(channelUUID.String()).Set(duration)
	}
}

func SetMsgStatusReceiveByType(channelType string, duration float64) {
	msg_status_receive_by_type.WithLabelValues(channelType).Set(duration)
}

func SetMsgStatusReceiveByUUID(channelUUID uuid.UUID, duration float64) {
	if monitorAllChannels || channelsToMonitor[channelUUID] {
		msg_status_receive_by_uuid.WithLabelValues(channelUUID.String()).Set(duration)
	}
}

func SetBulkQueueSize(size float64) {
	bulk_queue_size.Set(size)
}

func SetPriorityQueueSize(size float64) {
	priority_queue_size.Set(size)
}

func SetNewContactsCount(count float64) {
	new_contacts_count.Set(count)
}

func SetNewContactsByType(channelType string, count float64) {
	new_contacts_by_type.WithLabelValues(channelType).Set(count)
}

func SetNewContactsByUUID(channelUUID uuid.UUID, count float64) {
	if monitorAllChannels || channelsToMonitor[channelUUID] {
		new_contacts_by_uuid.WithLabelValues(channelUUID.String()).Set(count)
	}
}
