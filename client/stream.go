package client

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/lbryio/transcoder/video"
)

type CachedVideo struct {
	dirName string
	size    int64
}

type Downloadable interface {
	Download() error
	Progress() <-chan Progress
}

type Progress struct {
	Error       error
	Stage       int
	Done        bool
	BytesLoaded int64
}

type HLSStream struct {
	URL          string
	size         int64
	SDHash       string
	client       *Client
	progress     chan Progress
	filesFetched int
}

func (v *CachedVideo) Size() int64 {
	return v.size
}

func (v *CachedVideo) DirName() string {
	return v.dirName
}

func (v CachedVideo) delete() error {
	return os.RemoveAll(v.DirName())
}

func newHLSStream(url, sdHash string, client *Client) *HLSStream {
	s := &HLSStream{URL: url, progress: make(chan Progress, 1), client: client, SDHash: sdHash}
	return s
}

func (s HLSStream) fetch(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return s.client.httpClient.Do(req)
}

func (s HLSStream) retrieveFile(rootPath ...string) ([]byte, error) {
	rawurl := strings.Join(rootPath, "/")

	logger.Debugw("retrieving file media", "url", rawurl)
	_, err := url.Parse(rawurl)
	if err != nil {
		return nil, err
	}

	res, err := s.fetch(rawurl)
	defer res.Body.Close()
	if err != nil {
		return nil, err
	}

	data, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	s.makeProgress(int64(len(data)))
	return data, nil
}

func (s HLSStream) saveFile(data []byte, name string) error {
	logger.Debugw("saving file", "path", path.Join(s.LocalPath(), name))
	err := ioutil.WriteFile(path.Join(s.LocalPath(), name), data, os.ModePerm)
	if err != nil {
		return err
	}

	return nil
}

func (s HLSStream) Download() error {
	logger.Debugw("stream download requested", "url", s.rootURL())
	res, err := s.fetch(s.rootURL())
	if err != nil {
		return err
	}

	logger.Debugw("transcoder response", "status", res.StatusCode)
	switch res.StatusCode {
	case http.StatusForbidden:
		return video.ErrChannelNotEnabled
	case http.StatusNotFound:
		return errors.New("stream not found")
	case http.StatusAccepted:
		return errors.New("encoding underway")
	case http.StatusSeeOther:
		loc, err := res.Location()
		if err != nil {
			return err
		}
		go func() {
			err := s.startDownload(loc.String())
			if err != nil {
				s.progress <- Progress{Error: err}
			}
		}()
		return nil
	default:
		return fmt.Errorf("unknown http status: %v", res.StatusCode)
	}
}

func (s HLSStream) Progress() <-chan Progress {
	return s.progress
}

func (s *HLSStream) makeProgress(bl int64) {
	s.filesFetched++
	s.progress <- Progress{Stage: s.filesFetched, BytesLoaded: bl}
}

func (s *HLSStream) startDownload(playlistURL string) error {
	if !s.client.canStartDownload(s.rootURL()) {
		return errors.New("download already in progress")
	}

	rootPath := strings.Replace(playlistURL, "/"+MasterPlaylistName, "", 1)

	if err := os.MkdirAll(s.LocalPath(), os.ModePerm); err != nil {
		return err
	}

	streamSize, err := hlsPlaylistDive(rootPath, s.retrieveFile, s.saveFile)
	if err != nil {
		return err
	}

	s.progress <- Progress{Stage: 999999, BytesLoaded: streamSize}

	// Download complete
	logger.Debugw("got all files, saving to cache",
		"url", s.URL,
		"size", streamSize,
		"path", s.LocalPath(),
		"key", hlsCacheKey(s.SDHash),
	)
	s.client.CacheVideo(s.DirName(), streamSize)

	s.client.releaseDownload(s.rootURL())

	s.progress <- Progress{Done: true}
	close(s.progress)
	return nil
}

func (s HLSStream) rootURL() string {
	return fmt.Sprintf(hlsURLTemplate, s.client.server, s.SafeURL())
}

func (s HLSStream) SafeURL() string {
	return url.PathEscape(s.URL)
}

func (s HLSStream) LocalPath() string {
	return path.Join(s.client.videoPath, s.DirName())
}

func (s HLSStream) DirName() string {
	return s.SDHash
}
