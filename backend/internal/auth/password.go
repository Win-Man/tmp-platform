package auth

import (
	"errors"
	"regexp"

	"golang.org/x/crypto/bcrypt"
)

// BcryptCost 密码哈希成本（已定 Cost=10）。
const BcryptCost = 10

var (
	letterRe = regexp.MustCompile(`[A-Za-z]`)
	digitRe  = regexp.MustCompile(`[0-9]`)
)

// ErrWeakPassword 密码不满足策略。
var ErrWeakPassword = errors.New("密码需至少 8 位且同时包含字母与数字")

// HashPassword 生成 bcrypt 哈希。
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), BcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// VerifyPassword 校验明文与哈希是否匹配。
func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// ValidatePasswordPolicy 校验密码策略：≥8 位且含字母与数字。
func ValidatePasswordPolicy(plain string) error {
	if len(plain) < 8 || len(plain) > 128 {
		return ErrWeakPassword
	}
	if !letterRe.MatchString(plain) || !digitRe.MatchString(plain) {
		return ErrWeakPassword
	}
	return nil
}
