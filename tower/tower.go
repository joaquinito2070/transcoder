package tower

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/lbryio/transcoder/manager"
	"github.com/lbryio/transcoder/pkg/logging"
	"github.com/lbryio/transcoder/tower/metrics"
	"github.com/lbryio/transcoder/tower/queue"

	"github.com/fasthttp/router"
	"github.com/prometheus/client_golang/prometheus"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/pprofhandler"
)

const (
	TWorkerWait          = "worker_wait"
	TRequestPick         = "request_pick"
	TRequestSweep        = "request_sweep"
	TRequestHeartbeat    = "request_heartbeat"
	TWorkerStatus        = "worker_status"
	TWorkerStatusTimeout = "worker_status_timeout"
	TRequestTimeoutBase  = "request_timeout_base"
)

type ServerConfig struct {
	rmqAddr                 string
	db                      *sql.DB
	workDir, workDirUploads string
	httpServerBind          string
	HttpServerURL           string
	log                     logging.KVLogger
	videoManager            *manager.VideoManager
	managerToken            string
	timings                 map[string]time.Duration
	devMode                 bool
}

type Server struct {
	*ServerConfig
	rpc      *towerRPC
	stopChan chan struct{}

	httpServer *fasthttp.Server

	backCh *amqp.Channel
}

type worker struct {
	id        string
	capacity  int
	available int
	lastSeen  time.Time
}

type Timings map[string]time.Duration

func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		rmqAddr:        "amqp://guest:guest@localhost/",
		workDir:        ".",
		httpServerBind: ":18080",
		log:            logging.NoopKVLogger{},
		timings:        defaultTimings(),
	}
}

func (c *ServerConfig) Logger(logger logging.KVLogger) *ServerConfig {
	c.log = logger
	return c
}

func (c *ServerConfig) Timings(t Timings) *ServerConfig {
	for k, v := range t {
		c.timings[k] = v
	}
	return c
}

func (c *ServerConfig) HttpServer(bind, url string) *ServerConfig {
	c.httpServerBind = bind
	c.HttpServerURL = strings.TrimRight(url, "/")
	return c
}

func (c *ServerConfig) VideoManager(manager *manager.VideoManager) *ServerConfig {
	c.videoManager = manager
	return c
}

func (c *ServerConfig) ManagerToken(token string) *ServerConfig {
	c.managerToken = token
	return c
}

func (c *ServerConfig) WorkDir(workDir string) *ServerConfig {
	c.workDir = workDir
	return c
}

func (c *ServerConfig) RMQAddr(addr string) *ServerConfig {
	c.rmqAddr = addr
	return c
}

func (c *ServerConfig) DB(db *sql.DB) *ServerConfig {
	c.db = db
	return c
}

func (c *ServerConfig) DevMode() *ServerConfig {
	c.devMode = true
	return c
}

func NewServer(config *ServerConfig) (*Server, error) {
	var err error

	s := Server{
		ServerConfig: config,
		stopChan:     make(chan struct{}),
	}

	if config.db == nil {
		return nil, errors.New("SQL DB not set")
	}

	s.workDirUploads = path.Join(s.workDir, "uploads")
	tl, err := newTaskList(queue.New(config.db))
	if err != nil {
		return nil, err
	}
	s.rpc, err = newTowerRPC(s.rmqAddr, tl, s.log)
	if err != nil {
		return nil, err
	}

	return &s, nil
}

func (s *Server) StartAll() error {
	if s.videoManager == nil {
		return errors.New("VideoManager is not configured")
	}

	s.rpc.declareQueues()

	if err := s.startForwardingRequests(s.videoManager.Requests()); err != nil {
		return err
	}
	if err := s.startHttpServer(); err != nil {
		return err
	}
	return nil
}

func (s *Server) StopAll() {
	s.log.Info("shutting down tower")
	close(s.stopChan)
	if s.rpc != nil {
		s.rpc.consumer.Close()
		s.rpc.publisher.Close()
	}
}

func (s *Server) startForwardingRequests(requests <-chan *manager.TranscodingRequest) error {
	activeTaskChan, err := s.rpc.startConsumingWorkRequests()
	if err != nil {
		return err
	}

	go func() {
		defer func() {
			s.log.Info("task dispatcher: QUIT")
		}()
		for {
			select {
			case at := <-activeTaskChan:
				ll := s.log.With("tid", at.id, "wid", at.workerID)
				if at.restored && at.exPayload != nil {
					ll.Info("task dispatcher: restored task")
					at.SendPayload(at.exPayload)
				} else {
					ll.Info("task dispatcher: new task")
					var mtt *MsgTranscodingTask
					for {
						ll.Info("task dispatcher: getting task from the pool")
						trReq := <-requests
						mtt = &MsgTranscodingTask{
							URL:    trReq.URI,
							SDHash: trReq.SDHash,
						}
						_, err = s.rpc.tasks.q.GetTaskBySDHash(context.Background(), mtt.SDHash)
						if err != nil {
							break
						}
						ll.Info("task dispatcher: duplicate task, rejected", "payload", mtt)
						trReq.Reject()
					}

					ll.Info("task dispatcher: sending payload", "payload", mtt)
					at.SendPayload(mtt)
				}
				// Timing out a at means it will be shipped back to the queue again
				// ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
				// defer cancel()
				go s.manageTask(at)
			case <-s.stopChan:
				return
			default:
				time.Sleep(50 * time.Millisecond)
			}
		}
	}()

	return nil
}

// func (s *Server) manageTask(ctx context.Context, at *activeTask) {
func (s *Server) manageTask(at *activeTask) {
	ll := s.log.With("tid", at.id, "wid", at.workerID)
	labels := prometheus.Labels{metrics.LabelWorkerName: at.workerID}
	metrics.TranscodingRequestsRunning.With(labels).Inc()
	defer metrics.TranscodingRequestsRunning.With(labels).Dec()
	ll.Info("task dispatcher: managing task", "restored", at.restored)
	for {
		select {
		case p := <-at.progress:
			ll.Info("progress received", "progress", p.Percent, "stage", p.Stage)
		case e := <-at.errors:
			ll.Error("task errored", "err", e)
			metrics.TranscodingRequestsErrors.With(labels).Inc()
			return
		case d := <-at.success:
			m := d.RemoteStream.Manifest
			if m == nil {
				ll.Error("remote stream missing manifest", "task", fmt.Sprintf("%+v", at))
				metrics.TranscodingRequestsErrors.With(labels).Inc()
				return
			}
			metrics.TranscodingRequestsRunning.With(prometheus.Labels{metrics.LabelWorkerName: at.workerID}).Dec()
			if err := s.videoManager.Library().AddRemoteStream(*d.RemoteStream); err != nil {
				ll.Info("error adding remote stream", "err", err, "stream", d.RemoteStream)
				metrics.TranscodingRequestsErrors.With(labels).Inc()
				return
			}
			ll.Info("added remote stream", "url", d.RemoteStream.URL())
			metrics.TranscodingRequestsDone.With(labels).Inc()
			return
		case <-time.After(300 * time.Second):
			ll.Warn("timed out waiting for worker status")
		case <-s.stopChan:
			return
		}
	}
}

func (s *Server) startHttpServer() error {
	router := router.New()

	metrics.RegisterTowerMetrics()

	if s.managerToken == "" {
		return errors.New("manager token not set")
	}
	manager.CreateRoutes(router, s.videoManager, s.log, func(ctx *fasthttp.RequestCtx) bool {
		return ctx.UserValue(manager.TokenCtxField).(string) == s.managerToken
	})

	router.GET("/debug/pprof/{profile:*}", pprofhandler.PprofHandler)

	s.log.Info("starting tower http server", "addr", s.httpServerBind, "url", s.HttpServerURL)
	// TODO: Cleanup middleware attachment.
	httpServer := &fasthttp.Server{
		Handler:          manager.MetricsMiddleware(manager.CORSMiddleware(router.Handler)),
		Name:             "tower",
		DisableKeepalive: true,
	}
	// s.upAddr = l.Addr().String()

	s.httpServer = httpServer
	go func() {
		err := httpServer.ListenAndServe(s.httpServerBind)
		if err != nil {
			s.log.Error("http server error", "err", err)
			close(s.stopChan)
		}
	}()
	go func() {
		<-s.stopChan
		s.log.Info("shutting down tower http server", "addr", s.httpServerBind, "url", s.HttpServerURL)
		httpServer.Shutdown()
	}()

	return nil
}

func defaultTimings() Timings {
	return Timings{
		TWorkerWait:          1000 * time.Millisecond,
		TRequestPick:         500 * time.Millisecond,
		TRequestSweep:        10 * time.Second,
		TWorkerStatusTimeout: 10 * time.Second,
		TRequestTimeoutBase:  1 * time.Minute,
		// Below are used by both server and worker
		TRequestHeartbeat: 10 * time.Second,
		TWorkerStatus:     300 * time.Millisecond,
	}
}
