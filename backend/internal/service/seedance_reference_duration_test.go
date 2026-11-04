//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type seedanceReferenceRoundTripper func(*http.Request) (*http.Response, error)

func (f seedanceReferenceRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type seedanceReferenceAssetUpstream struct {
	HTTPUpstream
	do func(*http.Request, string, int64, int) (*http.Response, error)
}

func (s *seedanceReferenceAssetUpstream) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	return s.do(req, proxy, id, concurrency)
}

func seedanceReferenceBox(kind string, body ...[]byte) []byte {
	payload := bytes.Join(body, nil)
	box := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(box, uint32(len(box)))
	copy(box[4:8], kind)
	copy(box[8:], payload)
	return box
}

// The audio track is deliberately longer: billing needs the video track's
// presentation duration, rather than the whole container's longest track.
func seedanceReferenceMovieFixture(timescale uint32, ticks uint64, version byte) []byte {
	movieHeader := make([]byte, 20)
	trackHeader := make([]byte, 24)
	mediaHeader := make([]byte, 20)
	if version == 1 {
		movieHeader = make([]byte, 32)
		trackHeader = make([]byte, 36)
		mediaHeader = make([]byte, 32)
	}
	movieHeader[0], trackHeader[0], mediaHeader[0] = version, version, version
	if version == 1 {
		binary.BigEndian.PutUint32(movieHeader[20:], timescale)
		binary.BigEndian.PutUint64(movieHeader[24:], ticks*2)
		binary.BigEndian.PutUint64(trackHeader[28:], ticks)
		binary.BigEndian.PutUint32(mediaHeader[20:], timescale)
		binary.BigEndian.PutUint64(mediaHeader[24:], ticks)
	} else {
		binary.BigEndian.PutUint32(movieHeader[12:], timescale)
		binary.BigEndian.PutUint32(movieHeader[16:], uint32(ticks*2))
		binary.BigEndian.PutUint32(trackHeader[20:], uint32(ticks))
		binary.BigEndian.PutUint32(mediaHeader[12:], timescale)
		binary.BigEndian.PutUint32(mediaHeader[16:], uint32(ticks))
	}
	handler := make([]byte, 12)
	copy(handler[8:], "vide")
	video := seedanceReferenceBox("trak", seedanceReferenceBox("tkhd", trackHeader), seedanceReferenceBox("mdia", seedanceReferenceBox("mdhd", mediaHeader), seedanceReferenceBox("hdlr", handler)))
	audioHeader := append([]byte(nil), trackHeader...)
	audioHandler := append([]byte(nil), handler...)
	copy(audioHandler[8:], "soun")
	if version == 1 {
		binary.BigEndian.PutUint64(audioHeader[28:], ticks*2)
	} else {
		binary.BigEndian.PutUint32(audioHeader[20:], uint32(ticks*2))
	}
	audio := seedanceReferenceBox("trak", seedanceReferenceBox("tkhd", audioHeader), seedanceReferenceBox("mdia", seedanceReferenceBox("hdlr", audioHandler)))
	return bytes.Join([][]byte{
		seedanceReferenceBox("ftyp", []byte("isom\x00\x00\x00\x00isom")),
		seedanceReferenceBox("mdat", []byte("video payload")),
		seedanceReferenceBox("moov", seedanceReferenceBox("mvhd", movieHeader), video, audio),
	}, nil)
}

func seedanceReferenceResponse(payload []byte) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"video/mp4"}}, Body: io.NopCloser(bytes.NewReader(payload)), ContentLength: int64(len(payload))}
}

func TestSeedanceReferenceDurationSumsPublicVideos(t *testing.T) {
	seen := 0
	client := &http.Client{Transport: seedanceReferenceRoundTripper(func(req *http.Request) (*http.Response, error) {
		seen++
		require.Empty(t, req.Header.Get("Authorization"), "supplier credentials must never reach the material URL")
		switch req.URL.Path {
		case "/first.mp4":
			return seedanceReferenceResponse(seedanceReferenceMovieFixture(1000, 2500, 0)), nil
		case "/second.mov":
			return seedanceReferenceResponse(seedanceReferenceMovieFixture(1000, 3750, 1)), nil
		default:
			return nil, fmt.Errorf("unexpected URL")
		}
	})}
	info := &GrokMediaRequestInfo{HasReferenceVideo: true, InputVideoURLs: []string{"https://cdn.example/first.mp4", "https://cdn.example/second.mov"}}
	seconds, err := (&OpenAIGatewayService{}).resolveSeedanceReferenceVideoDuration(context.Background(), seedanceTestAccount(), info, client)
	require.NoError(t, err)
	require.Equal(t, 6.25, seconds)
	require.Equal(t, seconds, info.ReferenceVideoDurationSeconds)
	require.Equal(t, 2, seen)
	require.Equal(t, []string{"https://cdn.example/first.mp4", "https://cdn.example/second.mov"}, info.InputVideoURLs)
}

func TestSeedanceReferenceDurationResolvesAssetWithCurrentAccount(t *testing.T) {
	account := seedanceTestAccount()
	account.Credentials["base_url"] = "https://api.newtoken.club/proxy/api/v3"
	account.Concurrency = 7
	upstream := &seedanceReferenceAssetUpstream{do: func(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "https://api.newtoken.club/proxy/api/v3/asset/get", req.URL.String())
		require.Equal(t, "Bearer ark-secret", req.Header.Get("Authorization"))
		require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
		require.Empty(t, proxy)
		require.Equal(t, account.ID, id)
		require.Equal(t, account.Concurrency, concurrency)
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"id":"ast-reference"}`, string(body))
		return grokMediaContentStatusResponse(`{"success":true,"result":{"Id":"ast-reference","AssetType":"Video","Status":"Active","URL":"https://cdn.example/signed.mov?sig=material-secret"}}`), nil
	}}
	client := &http.Client{Transport: seedanceReferenceRoundTripper(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "https://cdn.example/signed.mov?sig=material-secret", req.URL.String())
		require.Empty(t, req.Header.Get("Authorization"))
		return seedanceReferenceResponse(seedanceReferenceMovieFixture(1000, 4250, 1)), nil
	})}
	info := &GrokMediaRequestInfo{HasReferenceVideo: true, InputVideoURLs: []string{"asset://ast-reference"}}
	seconds, err := (&OpenAIGatewayService{httpUpstream: upstream}).resolveSeedanceReferenceVideoDuration(context.Background(), account, info, client)
	require.NoError(t, err)
	require.Equal(t, 4.25, seconds)
	require.Equal(t, []string{"asset://ast-reference"}, info.InputVideoURLs, "native upstream material reference is unchanged")
}

func TestSeedanceReferenceDurationRejectsUnsafeAndInvalidInputs(t *testing.T) {
	for _, raw := range []string{"file:///tmp/input.mp4", "https://user:secret@cdn.example/video.mp4", "http://127.0.0.1/video.mp4", "http://[::1]/video.mp4", "https://localhost/video.mp4", "http://169.254.169.254/video.mp4", "https://10.0.0.1/video.mp4", "asset://ast-reference/not-id"} {
		t.Run(raw, func(t *testing.T) {
			client := &http.Client{Transport: seedanceReferenceRoundTripper(func(*http.Request) (*http.Response, error) {
				t.Fatal("unsafe URL must be rejected before download")
				return nil, nil
			})}
			info := &GrokMediaRequestInfo{HasReferenceVideo: true, InputVideoURLs: []string{raw}, ReferenceVideoDurationSeconds: 99}
			_, err := (&OpenAIGatewayService{}).resolveSeedanceReferenceVideoDuration(context.Background(), seedanceTestAccount(), info, client)
			require.Error(t, err)
			require.Zero(t, info.ReferenceVideoDurationSeconds)
		})
	}
	for _, body := range [][]byte{[]byte("not a movie"), seedanceReferenceMovieFixture(0, 5000, 0), seedanceReferenceMovieFixture(1000, 0, 1), seedanceReferenceBox("moov", []byte{1, 2, 3}), seedanceReferenceBox("moov", seedanceReferenceBox("mvhd", make([]byte, 20)))} {
		client := &http.Client{Transport: seedanceReferenceRoundTripper(func(*http.Request) (*http.Response, error) { return seedanceReferenceResponse(body), nil })}
		info := &GrokMediaRequestInfo{HasReferenceVideo: true, InputVideoURLs: []string{"https://cdn.example/video.mp4"}}
		_, err := (&OpenAIGatewayService{}).resolveSeedanceReferenceVideoDuration(context.Background(), seedanceTestAccount(), info, client)
		require.Error(t, err)
		require.Zero(t, info.ReferenceVideoDurationSeconds)
	}
}

func TestSeedanceReferenceDurationRequiresAllInputsToBeMeasured(t *testing.T) {
	client := &http.Client{Transport: seedanceReferenceRoundTripper(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "bad.mp4") {
			return seedanceReferenceResponse([]byte("not a movie")), nil
		}
		return seedanceReferenceResponse(seedanceReferenceMovieFixture(1000, 2500, 0)), nil
	})}
	info := &GrokMediaRequestInfo{HasReferenceVideo: true, InputVideoURLs: []string{"https://cdn.example/good.mp4", "https://cdn.example/bad.mp4"}, ReferenceVideoDurationSeconds: 99}
	_, err := (&OpenAIGatewayService{}).resolveSeedanceReferenceVideoDuration(context.Background(), seedanceTestAccount(), info, client)
	require.ErrorContains(t, err, "reference video 2")
	require.Zero(t, info.ReferenceVideoDurationSeconds, "a partial sum must never be billed")
}

func TestSeedanceReferenceDurationRejectsUnavailableAssetsWithoutLeakingCredentials(t *testing.T) {
	for _, response := range []string{
		`{"success":false,"message":"ark-secret"}`,
		`{"success":true,"result":{"Id":"ast-reference","AssetType":"Video","Status":"Processing","URL":"https://cdn.example/a.mp4"}}`,
		`{"success":true,"result":{"Id":"ast-reference","AssetType":"Image","Status":"Active","URL":"https://cdn.example/a.mp4"}}`,
		`{"success":true,"result":{"Id":"another-asset","AssetType":"Video","Status":"Active","URL":"https://cdn.example/a.mp4"}}`,
		`{"success":true,"result":{"Id":"ast-reference","AssetType":"Video","Status":"Active","URL":"http://127.0.0.1/a.mp4"}}`,
	} {
		t.Run(response, func(t *testing.T) {
			upstream := &seedanceReferenceAssetUpstream{do: func(*http.Request, string, int64, int) (*http.Response, error) {
				return grokMediaContentStatusResponse(response), nil
			}}
			client := &http.Client{Transport: seedanceReferenceRoundTripper(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid asset must never be downloaded")
				return nil, nil
			})}
			info := &GrokMediaRequestInfo{HasReferenceVideo: true, InputVideoURLs: []string{"asset://ast-reference"}}
			_, err := (&OpenAIGatewayService{httpUpstream: upstream}).resolveSeedanceReferenceVideoDuration(context.Background(), seedanceTestAccount(), info, client)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "ark-secret")
			require.Zero(t, info.ReferenceVideoDurationSeconds)
		})
	}
}

func TestSeedanceReferenceDurationBoundsDownload(t *testing.T) {
	client := &http.Client{Transport: seedanceReferenceRoundTripper(func(*http.Request) (*http.Response, error) {
		resp := seedanceReferenceResponse(seedanceReferenceMovieFixture(1000, 2500, 0))
		resp.ContentLength = 1 << 30
		return resp, nil
	})}
	info := &GrokMediaRequestInfo{HasReferenceVideo: true, InputVideoURLs: []string{"https://cdn.example/video.mp4"}}
	_, err := (&OpenAIGatewayService{}).resolveSeedanceReferenceVideoDuration(context.Background(), seedanceTestAccount(), info, client)
	require.ErrorContains(t, err, "too large")
}

func TestSeedanceReferenceDurationRejectsRedirectToPrivateAddress(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: &imageDownloadTransport{direct: seedanceReferenceRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "cdn.example", req.URL.Hostname())
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"http://169.254.169.254/latest/meta-data"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}}
	info := &GrokMediaRequestInfo{HasReferenceVideo: true, InputVideoURLs: []string{"https://cdn.example/video.mp4"}}
	_, err := (&OpenAIGatewayService{}).resolveSeedanceReferenceVideoDuration(context.Background(), seedanceTestAccount(), info, client)
	require.Error(t, err)
	require.Equal(t, 1, calls, "redirect destination must be rejected before a request is sent")
	require.Zero(t, info.ReferenceVideoDurationSeconds)
}

func TestSeedanceReferenceDurationRejectsInvalidBoxSizes(t *testing.T) {
	for _, fixture := range [][]byte{
		{0, 0, 0, 7, 'm', 'o', 'o', 'v'}, // Box smaller than its own header.
		{0, 0, 0, 1, 'm', 'o', 'o', 'v'}, // Extended size missing.
		{0, 0, 0, 1, 'm', 'o', 'o', 'v', 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		{0x01, 0x00, 0x00, 0x09, 'm', 'o', 'o', 'v'}, // Metadata exceeds its bound.
		{0, 0, 0, 16, 'm', 'd', 'a', 't', 0},         // Media payload truncated.
	} {
		_, err := seedanceReferenceMovieSeconds(bytes.NewReader(fixture))
		require.Error(t, err)
	}
}

func TestResolveSeedanceReferenceDurationNoInputAndMissingInput(t *testing.T) {
	svc := &OpenAIGatewayService{}
	seconds, err := svc.ResolveSeedanceReferenceVideoDuration(context.Background(), seedanceTestAccount(), &GrokMediaRequestInfo{})
	require.NoError(t, err)
	require.Zero(t, seconds)
	_, err = svc.ResolveSeedanceReferenceVideoDuration(context.Background(), seedanceTestAccount(), &GrokMediaRequestInfo{HasReferenceVideo: true})
	require.Error(t, err)
}
