package encoder

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lbryio/transcoder/ladder"
	"github.com/lbryio/transcoder/pkg/logging/zapadapter"
	"github.com/lbryio/transcoder/pkg/resolve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	c, err := resolve.ResolveStream(url)
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

func (s *encoderSuite) TestCheckFastStart() {
	absPath, _ := filepath.Abs(s.file.Name())
	e, err := NewEncoder(Configure().Log(zapadapter.NewKV(nil)).Ladder(ladder.Default))
	s.Require().NoError(err)
	m, err := e.GetMetadata(absPath)
	s.Require().NoError(err)
	s.True(m.FastStart)
}

func (s *encoderSuite) TestLadder() {
	url := "CahlenLee_20220123_GrapheneOSOnAPixel4XL#1a208b628290b2514b632958c623c08fc0c190d2"
	e, err := NewEncoder(Configure().Log(zapadapter.NewKV(nil)).Ladder(ladder.Default))
	s.Require().NoError(err)

	c, err := resolve.ResolveStream(url)
	s.Require().NoError(err)
	file, _, err := c.Download(s.T().TempDir())
	s.Require().NoError(err)
	file.Close()
	res, err := e.Encode(file.Name(), s.out)
	s.Require().NoError(err)
	s.Equal([]ladder.Tier{
		{Definition: "360p", Width: 640, Height: 360, VideoBitrate: 500_000, AudioBitrate: "96k", Framerate: 0},
		{Definition: "144p", Width: 256, Height: 144, VideoBitrate: 100_000, AudioBitrate: "64k", Framerate: 15},
	}, res.Ladder.Tiers)
}

func (s *encoderSuite) TestEncode() {
	absPath, _ := filepath.Abs(s.file.Name())
	e, err := NewEncoder(Configure().Log(zapadapter.NewKV(nil)).Ladder(ladder.Default))
	s.Require().NoError(err)

	res, err := e.Encode(absPath, s.out)
	s.Require().NoError(err)

	vs := res.OrigMeta.VideoStream
	s.Equal(1920, vs.GetWidth())
	s.Equal(1080, vs.GetHeight())

	progress := 0.0
	for p := range res.Progress {
		progress = p.GetProgress()
	}

	s.Require().GreaterOrEqual(progress, 99.5)

	s.Equal(1080, res.Ladder.Tiers[0].Height)
	s.Equal(720, res.Ladder.Tiers[1].Height)
	s.Equal(360, res.Ladder.Tiers[2].Height)
	s.Equal(144, res.Ladder.Tiers[3].Height)

	outFiles := map[string]string{
		"master.m3u8": `
#EXTM3U
#EXT-X-VERSION:6
#EXT-X-STREAM-INF:BANDWIDTH=4432120,RESOLUTION=1920x1080,CODECS="avc1.\w+,mp4a.40.2"
v0.m3u8

#EXT-X-STREAM-INF:BANDWIDTH=660000,RESOLUTION=1280x720,CODECS="avc1.\w+,mp4a.40.2"
v1.m3u8

#EXT-X-STREAM-INF:BANDWIDTH=105600,RESOLUTION=640x360,CODECS="avc1.\w+,mp4a.40.2"
v2.m3u8

#EXT-X-STREAM-INF:BANDWIDTH=70400,RESOLUTION=256x144,CODECS="avc1.\w+,mp4a.40.2"
v3.m3u8`,
		"v0.m3u8":       "v0_s000000.ts",
		"v1.m3u8":       "v1_s000000.ts",
		"v2.m3u8":       "v2_s000000.ts",
		"v3.m3u8":       "v3_s000000.ts",
		"v0_s000000.ts": "",
		"v1_s000000.ts": "",
		"v2_s000000.ts": "",
		"v3_s000000.ts": "",
	}
	for f, str := range outFiles {
		cont, err := ioutil.ReadFile(path.Join(s.out, f))
		s.NoError(err)
		s.Regexp(strings.TrimSpace(str), string(cont))
	}
}

func TestTweakRealStreams(t *testing.T) {
	t.Skip()
	encoder, err := NewEncoder(Configure().Log(zapadapter.NewKV(nil)).Ladder(ladder.Default))
	require.NoError(t, err)

	testCases := []struct {
		url           string
		expectedTiers []ladder.Tier
	}{
		{
			// "hot-tub-streamers-are-furious-at#06e0bc43f55fec0bd946a3cb18fc2ff9bc1cb2aa",
			"hot-tub-streamers-are-furious.mp4",
			[]ladder.Tier{
				{Width: 1920, Height: 1080, VideoBitrate: 3500_000, AudioBitrate: "160k", Framerate: 0},
				{Width: 1280, Height: 720, VideoBitrate: 2500_000, AudioBitrate: "128k", Framerate: 0},
				{Width: 640, Height: 360, VideoBitrate: 500_000, AudioBitrate: "96k", Framerate: 0},
				{Width: 256, Height: 144, VideoBitrate: 100_000, AudioBitrate: "64k", Framerate: 15},
			},
		},
		{
			// "hot-tub-streamers-are-furious-at#06e0bc43f55fec0bd946a3cb18fc2ff9bc1cb2aa",
			"why-mountain-biking-here-will.mp4",
			[]ladder.Tier{
				{Width: 1920, Height: 1080, VideoBitrate: 3500_000, AudioBitrate: "160k", Framerate: 0},
				{Width: 1280, Height: 720, VideoBitrate: 2500_000, AudioBitrate: "128k", Framerate: 0},
				{Width: 640, Height: 360, VideoBitrate: 500_000, AudioBitrate: "96k", Framerate: 0},
				{Width: 256, Height: 144, VideoBitrate: 100_000, AudioBitrate: "64k", Framerate: 15},
			},
		},
	}

	for _, tc := range testCases {
		// c, err := resolve.ResolveStream(tc.url)
		// require.NoError(t, err)
		// file, _, err := c.Download(t.TempDir())
		// require.NoError(t, err)
		// file.Close()

		absPath, err := filepath.Abs(filepath.Join("./testdata", tc.url))
		lmeta, err := encoder.GetMetadata(absPath)
		require.NoError(t, err)

		stream := ladder.GetVideoStream(lmeta.FMeta)
		testName := fmt.Sprintf(
			"%s (%vx%v@%vbps)",
			tc.url,
			stream.GetWidth(),
			stream.GetHeight(),
			lmeta.FMeta.GetFormat().GetBitRate(),
		)
		t.Run(testName, func(t *testing.T) {
			assert.NoError(t, err)
			targetLadder, err := ladder.Default.Tweak(lmeta)
			assert.NoError(t, err)
			assert.NoError(t, err)
			require.Equal(t, len(tc.expectedTiers), len(targetLadder.Tiers), targetLadder.Tiers)
			for i, tier := range targetLadder.Tiers {
				assert.Equal(t, tc.expectedTiers[i].Width, tier.Width, tier)
				assert.Equal(t, tc.expectedTiers[i].Height, tier.Height, tier)
				assert.Equal(t, tc.expectedTiers[i].VideoBitrate, tier.VideoBitrate, tier)
				assert.Equal(t, tc.expectedTiers[i].AudioBitrate, tier.AudioBitrate, tier)
				assert.Equal(t, tc.expectedTiers[i].Framerate, tier.Framerate, tier)
			}
		})
	}
}
