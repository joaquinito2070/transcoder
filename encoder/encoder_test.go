package encoder

import (
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lbryio/transcoder/formats"
	"github.com/lbryio/transcoder/manager"
	"github.com/stretchr/testify/suite"
)

type encoderSuite struct {
	suite.Suite
	file    *os.File
	in, out string
}

func TestEncoderSuite(t *testing.T) {
	suite.Run(t, new(encoderSuite))
}

func (s *encoderSuite) SetupSuite() {
	s.out = path.Join(os.TempDir(), "encoderSuite_out")
	s.in = path.Join(os.TempDir(), "encoderSuite_in")

	url := "@specialoperationstest#3/fear-of-death-inspirational#a"
	c, err := manager.ResolveRequest(url)
	if err != nil {
		panic(err)
	}
	s.file, _, err = c.Download(s.in)
	s.file.Close()
	s.Require().NoError(err)
}

func (s *encoderSuite) TearDownSuite() {
	os.Remove(s.file.Name())
	os.RemoveAll(s.out)
}

func (s *encoderSuite) TestEncode() {
	absPath, _ := filepath.Abs(s.file.Name())
	e, err := NewEncoder(Configure())
	s.Require().NoError(err)

	res, err := e.Encode(absPath, s.out)
	s.Require().NoError(err)

	vs := formats.GetVideoStream(res.Meta)
	s.Equal(1920, vs.GetWidth())
	s.Equal(1080, vs.GetHeight())

	progress := 0.0
	for p := range res.Progress {
		progress = p.GetProgress()
	}

	s.Require().GreaterOrEqual(progress, 99.5)

	s.Equal(formats.HD1080, res.Formats[0].Resolution)
	s.Equal(formats.HD720, res.Formats[1].Resolution)
	s.Equal(formats.SD360, res.Formats[2].Resolution)
	s.Equal(formats.SD144, res.Formats[3].Resolution)

	outFiles := map[string]string{
		"master.m3u8": `
#EXTM3U
#EXT-X-VERSION:6
#EXT-X-STREAM-INF:BANDWIDTH=3660800,RESOLUTION=1920x1080,CODECS="avc1.\w+,mp4a.40.2"
stream_0.m3u8

#EXT-X-STREAM-INF:BANDWIDTH=2340800,RESOLUTION=1280x720,CODECS="avc1.\w+,mp4a.40.2"
stream_1.m3u8

#EXT-X-STREAM-INF:BANDWIDTH=756800,RESOLUTION=640x360,CODECS="avc1.\w+,mp4a.40.2"
stream_2.m3u8

#EXT-X-STREAM-INF:BANDWIDTH=316800,RESOLUTION=256x144,CODECS="avc1.\w+,mp4a.40.2"
stream_3.m3u8`,
		"stream_0.m3u8":   "seg_0_000000.ts",
		"stream_1.m3u8":   "seg_1_000000.ts",
		"stream_2.m3u8":   "seg_2_000000.ts",
		"stream_3.m3u8":   "seg_3_000000.ts",
		"seg_0_000000.ts": "",
		"seg_1_000000.ts": "",
		"seg_2_000000.ts": "",
		"seg_3_000000.ts": "",
	}
	for f, str := range outFiles {
		cont, err := ioutil.ReadFile(path.Join(s.out, f))
		s.NoError(err)
		// m, err := regexp.MatchString(strings.TrimSpace(str), string(cont))
		// s.NoError(err)
		// s.True(m, fmt.Sprintf("%v doesn't match %v", string(cont), str))
		s.Regexp(strings.TrimSpace(str), string(cont))
	}
}
