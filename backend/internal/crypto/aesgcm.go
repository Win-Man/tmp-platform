// Package crypto 提供凭据加密能力：AES-256-GCM，密钥可来自 TMP_SECRET 或本地 secret.key 文件。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MaskedValue 接口层统一返回的掩码值。
const MaskedValue = "******"

// ErrEmptyCiphertext 密文为空。
var ErrEmptyCiphertext = errors.New("cipher: empty ciphertext")

// Cipher AES-256-GCM 加解密器。
type Cipher struct {
	aead cipher.AEAD
	key  []byte
}

// Load 加载或生成密钥并返回 Cipher。
//
// 密钥来源优先级：
//  1. secret 非空（TMP_SECRET，任意长度经 SHA-256 派生 32 字节）；
//  2. <dataDir>/secret.key 文件（不存在时生成 32 字节随机内容，权限 0600）；
//
// 返回 generated=true 表示本次新生成的文件密钥，调用方应打印 WARN 提示固定密钥。
func Load(secret, dataDir string) (c *Cipher, generated bool, err error) {
	var raw []byte
	if secret != "" {
		raw = []byte(secret)
	} else {
		keyPath := filepath.Join(dataDir, "secret.key")
		data, readErr := os.ReadFile(keyPath)
		if readErr == nil && len(data) >= 16 {
			raw = data
		} else if readErr != nil && !os.IsNotExist(readErr) {
			return nil, false, fmt.Errorf("读取密钥文件失败: %w", readErr)
		} else {
			buf := make([]byte, 32)
			if _, err = io.ReadFull(rand.Reader, buf); err != nil {
				return nil, false, fmt.Errorf("生成随机密钥失败: %w", err)
			}
			if err = os.MkdirAll(dataDir, 0o750); err != nil {
				return nil, false, fmt.Errorf("创建数据目录失败: %w", err)
			}
			if err = os.WriteFile(keyPath, buf, 0o600); err != nil {
				return nil, false, fmt.Errorf("写入密钥文件失败: %w", err)
			}
			raw = buf
			generated = true
		}
	}
	c, err = New(raw)
	return c, generated, err
}

// New 用原始密钥材料（经 SHA-256 派生）构造 Cipher。
func New(raw []byte) (*Cipher, error) {
	if len(raw) == 0 {
		return nil, errors.New("cipher: 密钥材料为空")
	}
	sum := sha256.Sum256(raw)
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead, key: sum[:]}, nil
}

// Encrypt 加密明文，返回 base64(nonce||ciphertext)。空明文返回空串（表示"未设置"）。
func (c *Cipher) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nil, nonce, []byte(plain), nil)
	buf := make([]byte, 0, len(nonce)+len(sealed))
	buf = append(buf, nonce...)
	buf = append(buf, sealed...)
	return base64.StdEncoding.EncodeToString(buf), nil
}

// Decrypt 解密 base64(nonce||ciphertext)；空密文返回空串。
func (c *Cipher) Decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("cipher: 密文格式错误: %w", err)
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return "", ErrEmptyCiphertext
	}
	plain, err := c.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("cipher: 解密失败: %w", err)
	}
	return string(plain), nil
}

// Mask 返回固定掩码；空值返回空串以区分「未设置」。
func Mask(v string) string {
	if v == "" {
		return ""
	}
	return MaskedValue
}
