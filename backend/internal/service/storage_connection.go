package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
)

func sameStorageCredentialTarget(endpoint, key, savedEndpoint, savedKey string) bool {
	return strings.TrimRight(strings.TrimSpace(endpoint), "/") == strings.TrimRight(strings.TrimSpace(savedEndpoint), "/") && strings.TrimSpace(key) == strings.TrimSpace(savedKey)
}

// ProbeObjectStorage creates and removes only a unique test object under the configured prefix.
func ProbeObjectStorage(ctx context.Context, prefix string,
	save func(context.Context, string, []byte) (string, error),
	open func(context.Context, string) (io.ReadCloser, error),
	remove func(context.Context, string) error,
) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	key := strings.Trim(strings.TrimSpace(prefix), "/")
	if key != "" {
		key += "/"
	}
	key += ".donebot-connectivity/" + uuid.NewString() + ".txt"
	data := []byte("DoneBot storage connectivity " + uuid.NewString())
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cleanupCancel()
		if cleanupErr := remove(cleanupCtx, key); cleanupErr != nil {
			message := fmt.Errorf("%s；测试对象：%s", storageConnectionError("删除测试文件", cleanupErr), key)
			err = errors.Join(err, message)
		}
	}()
	address, err := save(ctx, key, data)
	if err != nil {
		return storageConnectionError("写入测试文件", err)
	}
	reader, err := open(ctx, key)
	if err != nil {
		return storageConnectionError("读取测试文件", err)
	}
	if reader == nil {
		return fmt.Errorf("读取测试文件失败：服务未返回内容")
	}
	actual, readErr := io.ReadAll(io.LimitReader(reader, int64(len(data)+1)))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(actual, data) {
		return fmt.Errorf("读取测试文件失败：返回内容不一致，请检查存储桶及读取权限")
	}
	if address == "" {
		return nil
	}
	u, parseErr := url.Parse(address)
	if parseErr != nil || u.Host == "" || u.User != nil || u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("下载地址无效，请检查存储公网地址配置")
	}
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("下载地址无效，请检查存储公网地址配置")
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载地址访问失败，请检查公网域名、证书及预签名配置")
	}
	actual, readErr = io.ReadAll(io.LimitReader(resp.Body, int64(len(data)+1)))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载地址返回 HTTP %d，请检查公网域名、路径及访问权限", resp.StatusCode)
	}
	if readErr != nil || !bytes.Equal(actual, data) {
		return fmt.Errorf("下载地址内容不一致，请检查公网域名是否指向正确的存储桶")
	}
	return nil
}

func storageConnectionError(stage string, err error) error {
	var api smithy.APIError
	var ossErr *oss.ServiceError
	code := ""
	if errors.As(err, &api) {
		code = api.ErrorCode()
	} else if errors.As(err, &ossErr) {
		code = ossErr.Code
	}
	if code != "" {
		switch code {
		case "NoSuchBucket", "NotFound":
			return fmt.Errorf("%s失败：存储桶不存在，请检查 Bucket 和 Endpoint", stage)
		case "AccessDenied", "Forbidden":
			return fmt.Errorf("%s失败：访问被拒绝，请检查存储桶权限", stage)
		case "InvalidAccessKeyId", "SignatureDoesNotMatch", "InvalidToken", "ExpiredToken":
			return fmt.Errorf("%s失败：凭据或签名无效，请检查 Access Key、Secret、Region 和 Endpoint", stage)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%s超时或已取消，请检查 Endpoint 与网络", stage)
	}
	return fmt.Errorf("%s失败，请检查 Endpoint、存储桶、凭据与权限", stage)
}
