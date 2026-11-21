package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// ProbeTokenTTL 连接测试凭证有效期。
//
// 用户「测试连通性 → 提交接管」之间通常只有几十秒，10 分钟足够覆盖
// 「测试通过后翻看结果、补充信息再提交」的正常节奏，又不至于长期可重放。
const ProbeTokenTTL = 10 * time.Minute

// probeTokenSeparator 凭证时间戳与签名的分隔符。
const probeTokenSeparator = "."

// ProbeTokenSigner 无状态连接测试凭证签发器。
//
// 设计要点：不落库、不依赖会话，签发时把「连接参数指纹 + 时间戳」用
// HMAC-SHA256 签出，提交接管时按请求体重算指纹并校验签名与有效期，
// 因此服务重启后已签发的凭证依旧可用，且无法被篡改到另一组参数上。
type ProbeTokenSigner struct {
	key []byte
}

// NewProbeTokenSigner 由主密钥派生签名密钥（与凭据加密密钥用途隔离）。
func NewProbeTokenSigner(secret string) *ProbeTokenSigner {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("tmp/probe-token/v1"))
	return &ProbeTokenSigner{key: mac.Sum(nil)}
}

// ProbeFingerprint 计算连接参数指纹：sha256(tiupHost|tiupUser|sshPort|dbHost|dbPort|pdEndpoint)。
func ProbeFingerprint(tiupHost, tiupUser string, sshPort int, dbHost string, dbPort int, pdEndpoint string) string {
	raw := strings.Join([]string{
		NormalizeHost(tiupHost),
		strings.TrimSpace(tiupUser),
		strconv.Itoa(sshPort),
		NormalizeHost(dbHost),
		strconv.Itoa(dbPort),
		normalizePDEndpoint(pdEndpoint),
	}, "|")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Sign 为指纹签发凭证，格式 "<unixTs>.<hmac>"。
func (s *ProbeTokenSigner) Sign(fingerprint string, now time.Time) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	return ts + probeTokenSeparator + s.signature(fingerprint, ts)
}

// Verify 校验凭证：格式、时间窗、签名三项全部通过才算通过。
func (s *ProbeTokenSigner) Verify(fingerprint, token string, now time.Time) error {
	parts := strings.Split(token, probeTokenSeparator)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return errors.New("凭证格式不正确")
	}
	issuedUnix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return errors.New("凭证格式不正确")
	}
	issued := time.Unix(issuedUnix, 0)
	if now.Sub(issued) > ProbeTokenTTL {
		return errors.New("凭证已过期，请重新测试连通性")
	}
	// 容忍 1 分钟时钟偏差，避免容器与浏览器时间不同步导致误拒。
	if issued.Sub(now) > time.Minute {
		return errors.New("凭证时间异常")
	}
	expect := s.signature(fingerprint, parts[0])
	if !hmac.Equal([]byte(parts[1]), []byte(expect)) {
		return errors.New("连接参数已变更，请重新测试连通性")
	}
	return nil
}

// signature 计算 HMAC-SHA256 签名（base64url 无填充）。
func (s *ProbeTokenSigner) signature(fingerprint, ts string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(fingerprint))
	mac.Write([]byte("|"))
	mac.Write([]byte(ts))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// normalizePDEndpoint 归一化 PD 地址（去协议、去尾部斜杠、小写）。
func normalizePDEndpoint(endpoint string) string {
	v := strings.ToLower(strings.TrimSpace(endpoint))
	v = strings.TrimPrefix(v, "http://")
	v = strings.TrimPrefix(v, "https://")
	v = strings.TrimSuffix(v, "/")
	return v
}
