//go:build unit

package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const updateInstallationHelperMode = "DONEBOT_UPDATE_INSTALLATION_TEST_MODE"

// Exercise the real os.Executable/rename path in an isolated copy of the test
// executable. No production hooks or replacement of the parent test process.
func TestDoneBotReleaseInstallation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-place release installation uses Unix executable replacement")
	}
	originalPath, err := os.Executable()
	require.NoError(t, err)
	original, err := os.ReadFile(originalPath)
	require.NoError(t, err)

	for _, mode := range []string{"install", "checksum-mismatch", "corrupt-archive", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			executable := filepath.Join(dir, "sub2api")
			require.NoError(t, os.WriteFile(executable, original, 0755))
			if mode == "rollback" {
				require.NoError(t, os.WriteFile(executable+".backup", []byte("donebot-previous-release"), 0755))
			}
			cmd := exec.Command(executable, "-test.run=^TestDoneBotReleaseInstallationHelper$", "-test.v")
			cmd.Env = append(os.Environ(), updateInstallationHelperMode+"="+mode)
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)

			installed, err := os.ReadFile(executable)
			require.NoError(t, err)
			if mode == "install" {
				require.Equal(t, []byte("donebot-next-release"), installed)
				backup, err := os.ReadFile(executable + ".backup")
				require.NoError(t, err)
				require.Equal(t, sha256.Sum256(original), sha256.Sum256(backup))
				info, err := os.Stat(executable)
				require.NoError(t, err)
				require.Equal(t, os.FileMode(0755), info.Mode().Perm())
			} else if mode == "rollback" {
				require.Equal(t, []byte("donebot-previous-release"), installed)
				require.NoFileExists(t, executable+".backup")
			} else {
				require.Equal(t, sha256.Sum256(original), sha256.Sum256(installed),
					"failed verification or rollback must preserve the previous executable")
				require.NoFileExists(t, executable+".backup")
			}
			leftovers, err := filepath.Glob(filepath.Join(dir, ".sub2api-update-*"))
			require.NoError(t, err)
			require.Empty(t, leftovers, "staging files should be removed on success and failure")
		})
	}
}

func TestDoneBotReleaseInstallationHelper(t *testing.T) {
	mode := os.Getenv(updateInstallationHelperMode)
	if mode == "" {
		t.Skip("subprocess helper")
	}
	if mode == "rollback" {
		// The UI restarts after replacement before it offers rollback. On Linux,
		// os.Executable in the old process follows its inode to the renamed backup.
		svc := NewUpdateService(nil, nil, "0.4.0", "release")
		require.NoError(t, svc.Rollback())
		return
	}
	var archive bytes.Buffer
	compressed := gzip.NewWriter(&archive)
	tarball := tar.NewWriter(compressed)
	payload := []byte("donebot-next-release")
	require.NoError(t, tarball.WriteHeader(&tar.Header{Name: "sub2api", Mode: 0755, Size: int64(len(payload))}))
	_, err := tarball.Write(payload)
	require.NoError(t, err)
	require.NoError(t, tarball.Close())
	require.NoError(t, compressed.Close())
	if mode == "corrupt-archive" {
		archive.Reset()
		_, _ = archive.WriteString("not a gzip archive")
	}
	name := fmt.Sprintf("sub2api_0.4.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	baseURL := "https://github.com/Zluo003/donebot-api/releases/download/v0.4.0/"
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(archive.Bytes()), name)
	if mode == "checksum-mismatch" {
		checksum = strings.Repeat("0", 64) + "  " + name + "\n"
	}
	client := &updateInstallationClient{
		updateServiceGitHubClientStub: updateServiceGitHubClientStub{release: &GitHubRelease{
			TagName: "v0.4.0",
			Assets: []GitHubAsset{
				{Name: name, BrowserDownloadURL: baseURL + name},
				{Name: "checksums.txt", BrowserDownloadURL: baseURL + "checksums.txt"},
			},
		}},
		archive: archive.Bytes(), checksum: checksum,
	}
	svc := NewUpdateService(&updateServiceCacheStub{}, client, "0.3.0", "release")
	err = svc.PerformUpdate(context.Background())
	switch mode {
	case "checksum-mismatch":
		require.ErrorContains(t, err, "checksum verification failed")
	case "corrupt-archive":
		require.ErrorContains(t, err, "extraction failed")
	default:
		require.NoError(t, err)
	}
	require.Equal(t, githubRepo, client.latestRepo)
	require.True(t, client.checksumRead)
}

type updateInstallationClient struct {
	updateServiceGitHubClientStub
	archive      []byte
	checksum     string
	checksumRead bool
}

func (c *updateInstallationClient) DownloadFile(_ context.Context, _ string, dest string, _ int64) error {
	return os.WriteFile(dest, c.archive, 0600)
}

func (c *updateInstallationClient) FetchChecksumFile(context.Context, string) ([]byte, error) {
	c.checksumRead = true
	return []byte(c.checksum), nil
}
