package service

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeedanceDocumentedRequestModes(t *testing.T) {
	for _, tc := range []struct{ name, content, extra string }{
		{"text", `{"type":"text","text":"a cinematic sea"}`, `"duration":8,"ratio":"16:9"`},
		{"first_frame", `{"type":"text","text":"animate"},{"type":"image_url","image_url":{"url":"asset://ast-first"},"role":"first_frame"}`, `"ratio":"adaptive"`},
		{"first_last", `{"type":"text","text":"transition"},{"type":"image_url","image_url":{"url":"https://cdn.example/first.png?sig=abc"},"role":"first_frame"},{"type":"image_url","image_url":{"url":"asset://ast-last"},"role":"last_frame"}`, `"ratio":"adaptive"`},
		{"references", `{"type":"text","text":"create"},{"type":"image_url","image_url":{"url":"asset://ast-ref"},"role":"reference_image"},{"type":"video_url","video_url":{"url":"https://cdn.example/video.mp4"},"role":"reference_video"},{"type":"audio_url","audio_url":{"url":"asset://ast-audio"},"role":"reference_audio"}`, `"omni_reference_task_type":"reference","duration":12`},
		{"edit", `{"type":"text","text":"change colors"},{"type":"video_url","video_url":{"url":"asset://ast-video"},"role":"reference_video"}`, `"omni_reference_task_type":"edit","ratio":"adaptive","duration":-1`},
		{"extend", `{"type":"text","text":"continue"},{"type":"video_url","video_url":{"url":"https://cdn.example/source.mp4"},"role":"reference_video"}`, `"omni_reference_task_type":"extend","ratio":"adaptive","duration":30`},
		{"draft", `{"type":"text","text":"create"},{"type":"draft_task","draft_task":{"id":"draft-task"}}`, `"draft":false,"output_format":"mp4"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"model":"doubao-seedance-2-5-260628","content":[%s],"resolution":"1080p",%s}`, tc.content, tc.extra)
			info, err := ParseSeedanceRequest([]byte(body))
			require.NoError(t, err)
			require.Equal(t, "1080p", info.Resolution)
			require.NotEmpty(t, info.Prompt)
		})
	}
}

func TestSeedanceRejectsInvalidDocumentedCombinations(t *testing.T) {
	for _, body := range []string{
		`{"model":"doubao-seedance-2-0-260128","resolution":"","content":[{"type":"text","text":"ok"}]}`,
		`{"model":"doubao-seedance-2-0-fast-260128","resolution":"1080p","content":[{"type":"text","text":"ok"}]}`,
		`{"model":"doubao-seedance-2-5-260628","resolution":"4k","content":[{"type":"text","text":"ok"}]}`,
		`{"model":"doubao-seedance-2-5-260628","resolution":"720p","content":[{"type":"text","text":"  "}]}`,
		`{"model":"doubao-seedance-2-5-260628","resolution":"720p","content":[{"type":"text","text":"ok"},{"type":"image_url","image_url":{"url":"file:///etc/passwd"},"role":"first_frame"}]}`,
		`{"model":"doubao-seedance-2-5-260628","resolution":"720p","content":[{"type":"text","text":"ok"},{"type":"image_url","image_url":{"url":"asset://ast-last"},"role":"last_frame"}]}`,
		`{"model":"doubao-seedance-2-5-260628","resolution":"720p","content":[{"type":"text","text":"ok"},{"type":"image_url","image_url":{"url":"asset://a"},"role":"first_frame"},{"type":"image_url","image_url":{"url":"asset://b"},"role":"first_frame"}]}`,
		`{"model":"doubao-seedance-2-5-260628","resolution":"720p","content":[{"type":"text","text":"ok"},{"type":"image_url","image_url":{"url":"asset://a"},"role":"first_frame"},{"type":"video_url","video_url":{"url":"asset://b"},"role":"reference_video"}]}`,
		`{"model":"doubao-seedance-2-5-260628","resolution":"720p","omni_reference_task_type":"edit","ratio":"adaptive","duration":-1,"content":[{"type":"text","text":"ok"}]}`,
		`{"model":"doubao-seedance-2-5-260628","resolution":"720p","omni_reference_task_type":"edit","ratio":"16:9","duration":-1,"content":[{"type":"text","text":"ok"},{"type":"video_url","video_url":{"url":"asset://b"},"role":"reference_video"}]}`,
		`{"model":"doubao-seedance-2-0-260128","resolution":"720p","omni_reference_task_type":"extend","ratio":"adaptive","duration":30,"content":[{"type":"text","text":"ok"},{"type":"video_url","video_url":{"url":"asset://b"},"role":"reference_video"}]}`,
		`{"model":"doubao-seedance-2-5-260628","resolution":"720p","omni_reference_task_type":"extend","ratio":"adaptive","duration":3,"content":[{"type":"text","text":"ok"},{"type":"video_url","video_url":{"url":"asset://b"},"role":"reference_video"}]}`,
		`{"model":"doubao-seedance-2-0-260128","resolution":"720p","draft":true,"content":[{"type":"text","text":"ok"}]}`,
	} {
		_, err := ParseSeedanceRequest([]byte(body))
		require.Error(t, err, body)
	}
}

func TestSeedanceAccountResolutionAllowlist(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.newtoken.club", "openai_capabilities": []string{"seedance"}, "api_key": "test"}}
	model := "doubao-seedance-2-0-260128"
	require.True(t, account.SeedanceAllowsResolution(model, "4k"))
	require.False(t, account.SeedanceAllowsResolution(model, ""), "NewToken requires an explicit resolution")
	require.False(t, account.SeedanceAllowsResolution("doubao-seedance-2-0-fast-260128", "1080p"))
	account.Credentials["seedance_allowed_resolutions"] = []any{"720p", "1080p"}
	require.False(t, account.SeedanceAllowsResolution(model, "4k"))
	require.True(t, account.SeedanceAllowsResolution(model, "1080p"))
	account.Credentials["seedance_allowed_resolutions"] = []string{}
	require.False(t, account.SeedanceAllowsResolution(model, "720p"))
	account.Credentials["seedance_allowed_resolutions"] = "720p"
	require.False(t, account.SeedanceAllowsResolution(model, "720p"))
	delete(account.Credentials, "seedance_allowed_resolutions")
	account.Credentials["base_url"] = "https://ark.cn-beijing.volces.com/api/v3"
	require.True(t, account.SeedanceAllowsResolution("ep-existing", ""), "keep native Ark default compatibility")
	require.True(t, account.SeedanceAllowsResolution(model, ""), "native Ark defaults to 720p for known models too")
	account.Credentials["seedance_allowed_resolutions"] = []string{"1080p"}
	require.False(t, account.SeedanceAllowsResolution(model, ""), "the default resolution still obeys account restrictions")
}

func TestValidateSeedanceAllowedResolutions(t *testing.T) {
	values, err := ValidateSeedanceAllowedResolutions([]any{"720p", "1080p", "4k"})
	require.NoError(t, err)
	require.Equal(t, []string{"720p", "1080p", "4k"}, values)
	for _, value := range []any{nil, "720p", []any{"720p", 12}, []string{"1440p"}, []string{""}} {
		_, err := ValidateSeedanceAllowedResolutions(value)
		require.Error(t, err)
	}
}

func TestSeedanceOfficialResolutionMatrixAndNativeDefault(t *testing.T) {
	for _, tc := range []struct {
		model   string
		allowed []string
	}{
		{"doubao-seedance-2-0-260128", []string{"480p", "720p", "1080p", "4k"}},
		{"doubao-seedance-2-0-fast-260128", []string{"480p", "720p"}},
		{"doubao-seedance-2-0-mini-260615", []string{"480p", "720p"}},
		{"doubao-seedance-2-5-260628", []string{"480p", "720p", "1080p"}},
	} {
		for _, resolution := range tc.allowed {
			body := fmt.Sprintf(`{"model":%q,"resolution":%q,"content":[{"type":"text","text":"waves"}]}`, tc.model, resolution)
			_, err := ParseSeedanceRequest([]byte(body))
			require.NoError(t, err)
		}
		body := fmt.Sprintf(`{"model":%q,"content":[{"type":"text","text":"waves"}]}`, tc.model)
		info, err := ParseSeedanceRequest([]byte(body))
		require.NoError(t, err)
		require.Empty(t, info.Resolution, "do not synthesize the native default into forwarded fields")
	}
}
