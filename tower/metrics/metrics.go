package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	StorageLocal  = "local"
	StorageRemote = "remote"

	LabelWorkerName = "worker_name"
	LabelStage      = "stage"
)

var (
	once = sync.Once{}

	WorkersHeartbeats = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "workers_heartbeats",
	}, []string{LabelWorkerName})

	WorkersSpentSeconds = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "workers_spent_seconds",
	}, []string{LabelWorkerName, LabelStage})

	TranscodingRequestsPublished = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "transcoding_requests_published",
	})
	TranscodingRequestsRunning = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "transcoding_requests_running",
	}, []string{LabelWorkerName})
	TranscodingRequestsDone = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "transcoding_requests_done",
	}, []string{LabelWorkerName})

	TranscodingRequestsRetries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "transcoding_requests_retries",
	}, []string{LabelWorkerName})
	TranscodingRequestsErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "transcoding_requests_errors",
	}, []string{LabelWorkerName})
)

func RegisterTowerMetrics() {
	once.Do(func() {
		prometheus.MustRegister(
			WorkersSpentSeconds,
			TranscodingRequestsRunning, TranscodingRequestsRetries, TranscodingRequestsErrors, TranscodingRequestsDone,
		)
	})
}
