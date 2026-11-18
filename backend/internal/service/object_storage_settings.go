package service

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

func normalizeObjectStorageSettings(in *DoneBotStorageS3, prefix, driver string) {
	in.Endpoint = strings.TrimRight(strings.TrimSpace(in.Endpoint), "/")
	in.DownloadEndpoint = strings.TrimRight(strings.TrimSpace(in.DownloadEndpoint), "/")
	in.Region = strings.TrimSpace(in.Region)
	in.Bucket = strings.TrimSpace(in.Bucket)
	in.AccessKeyID = strings.TrimSpace(in.AccessKeyID)
	in.Prefix = strings.Trim(strings.TrimSpace(in.Prefix), "/")
	if in.Prefix == "" {
		in.Prefix = prefix
	}
	if in.Prefix != "" {
		in.Prefix += "/"
	}
	if in.Region == "" && driver == "s3" {
		in.Region = "auto"
	}
	if in.PublicBaseURL != nil {
		in.PublicBaseURL = doneBotStorageURL(strings.TrimRight(strings.TrimSpace(*in.PublicBaseURL), "/"))
	}
}

func validateObjectStorageURLs(in DoneBotStorageS3) error {
	public := ""
	if in.PublicBaseURL != nil {
		public = *in.PublicBaseURL
	}
	for name, value := range map[string]string{"Endpoint": in.Endpoint, "下载 Endpoint": in.DownloadEndpoint, "公网地址": public} {
		if value == "" {
			continue
		}
		u, err := url.Parse(value)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("%s 必须为完整的 HTTP(S) 地址", name)
		}
	}
	return nil
}

var ossRegionPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)+$`)
var ossBucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

func validateObjectStorageSettings(driver string, in DoneBotStorageS3) error {
	if err := validateObjectStorageURLs(in); err != nil {
		return err
	}
	if in.Bucket == "" || in.AccessKeyID == "" || in.SecretAccessKey == "" {
		return fmt.Errorf("请填写 Bucket、Access Key ID 和 Secret Access Key")
	}
	if driver != "oss" {
		return nil
	}
	if !ossRegionPattern.MatchString(in.Region) || strings.HasPrefix(in.Region, "oss-") {
		return fmt.Errorf("请填写 OSS 桶实际地域，如 cn-hangzhou，不要填写 auto 或 oss- 前缀")
	}
	if !ossBucketPattern.MatchString(in.Bucket) {
		return fmt.Errorf("OSS Bucket 名称格式无效")
	}
	regular := "oss-" + in.Region + ".aliyuncs.com"
	for name, endpoint := range map[string]string{"上传 Endpoint": in.Endpoint, "下载 Endpoint": in.DownloadEndpoint} {
		u, err := url.Parse(endpoint)
		valid := false
		if err == nil && u.Scheme == "https" && u.Path == "" {
			accelerated := u.Host == "oss-accelerate.aliyuncs.com" || u.Host == "oss-accelerate-overseas.aliyuncs.com"
			valid = u.Host == regular || name == "上传 Endpoint" && accelerated
		}
		if !valid {
			return fmt.Errorf("%s 必须是与 Region 对应的 OSS HTTPS 地域节点（不含 Bucket）；上传也可使用 OSS 加速节点", name)
		}
	}
	if in.PublicBaseURL != nil && *in.PublicBaseURL != "" {
		return fmt.Errorf("阿里云 OSS 使用私有桶和普通节点签名下载，请留空公网地址")
	}

	return nil
}

func sameObjectStorageCredentialTarget(in, saved DoneBotStorageS3) bool {
	return sameStorageCredentialTarget(in.Endpoint, in.AccessKeyID, saved.Endpoint, saved.AccessKeyID) && strings.TrimRight(in.DownloadEndpoint, "/") == strings.TrimRight(saved.DownloadEndpoint, "/")
}
