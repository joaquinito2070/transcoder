package manager

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Pallinder/go-randomdata"
	"github.com/lbryio/transcoder/library"
	"github.com/lbryio/transcoder/pkg/logging/zapadapter"

	"github.com/fasthttp/router"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/suite"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

type httpSuite struct {
	suite.Suite
	library.LibraryTestHelper
}

func TestHttp(t *testing.T) {
	suite.Run(t, new(httpSuite))
}

func (s *httpSuite) SetupTest() {
	s.Require().NoError(s.SetupLibraryDB())
}

func (s *httpSuite) TearDownTest() {
	s.Require().NoError(s.TearDownLibraryDB())
}

func (s *httpSuite) TestPromHttp() {
	QueueLength.With(prometheus.Labels{"queue": "common"}).Inc()
	QueueItemAge.With(prometheus.Labels{"queue": "common"}).Observe(125.12)
	QueueHits.With(prometheus.Labels{"queue": "common"}).Inc()
	RegisterMetrics()
	s.HTTPBodyContains(promhttp.Handler().ServeHTTP, http.MethodGet, "/metrics", nil, "transcoding_queue_item_age_seconds")
	s.HTTPBodyContains(promhttp.Handler().ServeHTTP, http.MethodGet, "/metrics", nil, "transcoding_queue_length")
	s.HTTPBodyContains(promhttp.Handler().ServeHTTP, http.MethodGet, "/metrics", nil, "transcoding_queue_hits")
}

func (s *httpSuite) TestAdmin() {
	router := router.New()

	ln := fasthttputil.NewInmemoryListener()
	server := &fasthttp.Server{
		Handler:            router.Handler,
		Name:               "tower",
		MaxRequestBodySize: 10 * 1024 * 1024 * 1024,
	}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return ln.Dial()
			},
		},
	}

	go func() {
		err := server.Serve(ln)
		if err != nil {
			s.FailNow("failed to serve: %v", err)
		}
	}()
	token := "test-token"
	lib := library.New(library.Config{DB: s.DB, Log: zapadapter.NewKV(nil)})
	mgr := NewManager(lib, 0)

	CreateRoutes(router, mgr, zapadapter.NewKV(nil), func(ctx *fasthttp.RequestCtx) bool {
		return ctx.UserValue(TokenCtxField).(string) == token
	})

	cases := []struct {
		data                         url.Values
		tokenHeader                  string
		statusCode                   int
		exURL, exClaimID, exResponse string
	}{
		{
			data:        url.Values{AdminChannelField: {"@specialoperationstest:3"}},
			tokenHeader: "Bearer " + token,
			statusCode:  http.StatusCreated,
			exURL:       "lbry://@specialoperationstest#3",
			exClaimID:   "395b0f23dcd07212c3e956b697ba5ba89578ca54",
		},
		{
			data:        url.Values{AdminChannelField: {"@specialoperationstest:3"}},
			tokenHeader: "Bearer " + token,
			statusCode:  http.StatusBadRequest,
			exResponse:  `.+duplicate key value violates unique constraint.+`,
		},
		{
			data:        url.Values{AdminChannelField: {randomdata.Alphanumeric(25)}},
			tokenHeader: "Bearer " + token,
			statusCode:  http.StatusBadRequest,
			exResponse:  `channel not found`,
		},
	}

	for _, c := range cases {
		s.Run(fmt.Sprintf("%+v", c.data), func() {
			req, err := http.NewRequest(http.MethodPost, "http://localhost/api/v1/channel", strings.NewReader(c.data.Encode()))
			s.Require().NoError(err)
			req.Header.Set(AuthHeader, c.tokenHeader)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp, err := client.Do(req)
			s.Require().NoError(err)

			rbody, err := ioutil.ReadAll(resp.Body)
			s.Require().NoError(err)
			s.Require().Equal(c.statusCode, resp.StatusCode, string(rbody))
			if c.exResponse != "" {
				s.Require().Regexp(regexp.MustCompile(c.exResponse), string(rbody))
			}

			channels, err := lib.GetAllChannels()
			s.Require().NoError(err)
			if c.exURL != "" {
				s.Require().Equal(c.exURL, channels[0].URL)
			}
			if c.exClaimID != "" {
				s.Require().Equal(c.exClaimID, channels[0].ClaimID)
			}
		})
	}

}
