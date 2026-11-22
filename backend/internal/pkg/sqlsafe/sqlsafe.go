// Package sqlsafe 提供 SQL 标识符白名单校验。
//
// TiDB 的用户名 / 库名 / Resource Group 名无法使用占位符，必须先做字符白名单与关键字
// 黑名单校验，再用反引号包裹拼接。
package sqlsafe

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrIllegalIdent 标识符不合法。
var ErrIllegalIdent = errors.New("sqlsafe: 标识符不合法")

// identRe 标识符允许字符：字母、数字、下划线，长度 1~64。
var identRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// hostRe 账号 Host 允许字符：字母、数字、点、下划线、百分号、冒号、连字符。
var hostRe = regexp.MustCompile(`^[A-Za-z0-9._%:\-]{1,255}$`)

// reserved 关键字黑名单，防止标识符语义越权。
var reserved = map[string]bool{
	"select": true, "insert": true, "update": true, "delete": true, "drop": true,
	"create": true, "alter": true, "grant": true, "revoke": true, "truncate": true,
	"union": true, "where": true, "from": true, "into": true, "set": true,
	"information_schema": true, "mysql": true, "performance_schema": true,
	"sys": true, "global": true, "session": true,
}

// Ident 校验标识符并返回反引号包裹的安全形式。
func Ident(name string) (string, error) {
	if !identRe.MatchString(name) {
		return "", fmt.Errorf("%w: %q", ErrIllegalIdent, name)
	}
	if reserved[strings.ToLower(name)] {
		return "", fmt.Errorf("%w: 保留字 %q 不允许作为标识符", ErrIllegalIdent, name)
	}
	return "`" + name + "`", nil
}

// IdentList 批量校验并返回逗号分隔的反引号形式。
func IdentList(names []string) (string, error) {
	if len(names) == 0 {
		return "", fmt.Errorf("%w: 空列表", ErrIllegalIdent)
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		q, err := Ident(n)
		if err != nil {
			return "", err
		}
		out = append(out, q)
	}
	return strings.Join(out, ", "), nil
}

// Host 校验账号 Host 模式并返回单引号包裹的字符串字面量（Host 是字符串而非标识符）。
func Host(h string) (string, error) {
	if !hostRe.MatchString(h) {
		return "", fmt.Errorf("%w: host %q", ErrIllegalIdent, h)
	}
	return "'" + h + "'", nil
}

// ValidIdent 仅做布尔校验，供前端/参数检查使用。
func ValidIdent(name string) bool {
	return identRe.MatchString(name) && !reserved[strings.ToLower(name)]
}

// privilegeWhitelist 允许授予的权限集合（PRD F-37）。
var privilegeWhitelist = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true,
	"CREATE": true, "DROP": true, "ALTER": true, "INDEX": true,
	"PROCESS": true, "SHOW DATABASES": true, "RELOAD": true, "REFERENCES": true,
	"CREATE TEMPORARY TABLES": true, "LOCK TABLES": true, "EXECUTE": true,
	"BACKUP_ADMIN": true, "PROCESSLIST": true, "SYSTEM_VARIABLES_ADMIN": true,
	"ALL": true, "ALL PRIVILEGES": true,
}

// Privilege 校验权限名是否在白名单内，返回大写形式。
//
// ALL / ALL PRIVILEGES 归一化为 "ALL PRIVILEGES"（TiDB 的 GRANT ALL 等价形式），
// 使拼接出的语句合法。
func Privilege(p string) (string, error) {
	up := strings.ToUpper(strings.TrimSpace(p))
	if !privilegeWhitelist[up] {
		return "", fmt.Errorf("%w: 权限 %q 不在白名单内", ErrIllegalIdent, p)
	}
	if up == "ALL" {
		return "ALL PRIVILEGES", nil
	}
	return up, nil
}
