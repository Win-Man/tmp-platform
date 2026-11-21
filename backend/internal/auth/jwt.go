// Package auth 提供 JWT 签发/校验、密码策略、RBAC 权限矩阵与 Gin 中间件。
package auth

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims 平台 JWT 载荷。
type Claims struct {
	// Ver 签发时的 platform_users.token_version；与库中不一致即视为已吊销。
	Ver int `json:"ver"`
	// Username 便于日志与审计使用（非鉴权依据）。
	Username string `json:"uname"`
	jwt.RegisteredClaims
}

// UserID 返回 sub 解析出的用户 ID。
func (c *Claims) UserID() int64 {
	var id int64
	_, err := fmt.Sscanf(c.Subject, "%d", &id)
	if err != nil {
		return 0
	}
	return id
}

// Manager JWT 管理器。
type Manager struct {
	key []byte
	ttl time.Duration
}

// NewManager 用主密钥与默认 TTL 构造管理器；密钥经 SHA-256 派生，salt 与 AES 不同。
func NewManager(secret string, ttl time.Duration) *Manager {
	sum := sha256.Sum256([]byte("tmp-jwt|" + secret))
	if ttl <= 0 {
		ttl = 8 * time.Hour
	}
	return &Manager{key: sum[:], ttl: ttl}
}

// TTL 返回当前 token 有效期。
func (m *Manager) TTL() time.Duration { return m.ttl }

// Issue 为指定用户签发 token，返回 token 与其过期时间。
func (m *Manager) Issue(userID int64, username string, tokenVersion int) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(m.ttl)
	claims := &Claims{
		Ver:      tokenVersion,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", userID),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        fmt.Sprintf("%d-%d", userID, now.UnixNano()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(m.key)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, exp, nil
}

// ErrInvalidToken token 无效（签名错误 / 过期 / 格式错误）。
var ErrInvalidToken = errors.New("token 无效或已过期")

// Parse 校验并解析 token。
func (m *Manager) Parse(raw string) (*Claims, error) {
	claims := &Claims{}
	tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("非预期的签名算法: %v", t.Header["alg"])
		}
		return m.key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !tok.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
