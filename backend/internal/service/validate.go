package service

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// clusterNamePattern 集群名白名单：以字母开头，3~40 位字母 / 数字 / 下划线 / 短横线。
//
// 该规则同时充当「集群名不得包含空格与中文」的防线，与 TiUP 集群名保持一致。
var clusterNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{2,39}$`)

const (
	// maxHostnameLength RFC1123 主机名总长度上限。
	maxHostnameLength = 253
	// maxHostLabelLength 单个标签长度上限。
	maxHostLabelLength = 63
)

// ValidateClusterName 校验集群名。
func ValidateClusterName(name string) error {
	v := strings.TrimSpace(name)
	if v == "" {
		return fmt.Errorf("集群名不能为空")
	}
	if len([]rune(v)) > 64 {
		return fmt.Errorf("集群名不超过 64 个字符")
	}
	if !clusterNamePattern.MatchString(v) {
		return fmt.Errorf("集群名格式不合法：需以字母开头，3~40 位字母、数字、下划线或短横线")
	}
	return nil
}

// ValidateHostField 校验主机字段（TiUP 主机 / 数据库主机）。
//
// 允许 IPv4、IPv6 与 RFC1123 主机名；field 为中文提示名。
func ValidateHostField(field, host string) error {
	v := strings.TrimSpace(host)
	if v == "" {
		return fmt.Errorf("%s不能为空", field)
	}
	if !isValidHost(v) {
		return fmt.Errorf("%s格式不合法：应为 IPv4/IPv6 地址或合法主机名（总长 ≤253，单段 ≤63，仅字母、数字与短横线）", field)
	}
	return nil
}

// ValidatePort 校验端口范围（1~65535）。
func ValidatePort(field string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%s需在 1~65535 之间", field)
	}
	return nil
}

// ValidatePDEndpoint 校验 PD 地址：host:port，可选 http(s):// 前缀。
func ValidatePDEndpoint(endpoint string) error {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return fmt.Errorf("PD 地址不能为空")
	}
	hostPort := raw
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("PD 地址格式不合法：应为 http(s)://host:port")
		}
		hostPort = u.Host
	}
	host, portText, err := net.SplitHostPort(hostPort)
	if err != nil {
		return fmt.Errorf("PD 地址格式不合法：应为 host:port，如 http://10.0.0.11:2379")
	}
	if !isValidHost(host) {
		return fmt.Errorf("PD 地址的主机部分不合法：应为 IPv4/IPv6 地址或合法主机名")
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return fmt.Errorf("PD 地址的端口需为数字")
	}
	if err := ValidatePort("PD 地址的端口", port); err != nil {
		return err
	}
	return nil
}

// ValidateHTTPURL 校验单个 http(s) 地址。
func ValidateHTTPURL(field, raw string) error {
	v := strings.TrimSpace(raw)
	if v == "" {
		return fmt.Errorf("%s不能为空", field)
	}
	u, err := url.Parse(v)
	if err != nil {
		return fmt.Errorf("%s格式不合法：无法解析该地址", field)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s必须以 http:// 或 https:// 开头", field)
	}
	if u.Host == "" {
		return fmt.Errorf("%s缺少主机名", field)
	}
	return nil
}

// ValidateURLList 校验地址列表（允许为空，非空时逐项校验）。
func ValidateURLList(field string, list []string) error {
	for _, item := range list {
		if strings.TrimSpace(item) == "" {
			return fmt.Errorf("%s包含空地址", field)
		}
		if err := ValidateHTTPURL(field, item); err != nil {
			return err
		}
	}
	return nil
}

// NormalizeHost 归一化主机名（去空白、去尾部点、小写）。
func NormalizeHost(host string) string {
	v := strings.ToLower(strings.TrimSpace(host))
	return strings.TrimSuffix(v, ".")
}

// isValidHost 判断是否为合法 IP 或 RFC1123 主机名。
func isValidHost(host string) bool {
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	h := strings.TrimSuffix(host, ".")
	if h == "" || len(h) > maxHostnameLength {
		return false
	}
	labels := strings.Split(h, ".")
	for _, label := range labels {
		if !isValidHostLabel(label) {
			return false
		}
	}
	// 纯数字标签（如 "999.1.1.1"）不是合法主机名，也不可能是 IP，直接拒绝。
	if allDigits(labels[len(labels)-1]) && len(labels) > 1 {
		return false
	}
	return true
}

// isValidHostLabel 校验单个 DNS 标签：1~63 位，首尾为字母或数字，中间可含短横线。
func isValidHostLabel(label string) bool {
	if label == "" || len(label) > maxHostLabelLength {
		return false
	}
	for i := 0; i < len(label); i++ {
		ch := label[i]
		isAlnum := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
		if i == 0 || i == len(label)-1 {
			if !isAlnum {
				return false
			}
			continue
		}
		if !isAlnum && ch != '-' {
			return false
		}
	}
	return true
}

// allDigits 判断字符串是否全为数字。
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
