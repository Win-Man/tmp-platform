package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tmp-platform/backend/internal/model"
)

// ---------- 密码 bcrypt ----------

func TestHashPassword_Verify(t *testing.T) {
	plain := "Passw0rd!x"
	hash, err := HashPassword(plain)
	if err != nil {
		t.Fatalf("HashPassword 失败: %v", err)
	}
	if hash == plain {
		t.Fatal("哈希结果不得等于明文")
	}
	if !VerifyPassword(hash, plain) {
		t.Fatal("正确明文应校验通过")
	}
	if VerifyPassword(hash, "wrong-pass-1") {
		t.Fatal("错误明文不应校验通过")
	}
}

func TestHashPassword_SaltUnique(t *testing.T) {
	a, err := HashPassword("SamePass1")
	if err != nil {
		t.Fatalf("HashPassword 失败: %v", err)
	}
	b, err := HashPassword("SamePass1")
	if err != nil {
		t.Fatalf("HashPassword 失败: %v", err)
	}
	if a == b {
		t.Fatal("同一明文的两次 bcrypt 哈希必须不同（含随机 salt）")
	}
	if !VerifyPassword(a, "SamePass1") || !VerifyPassword(b, "SamePass1") {
		t.Fatal("两种 salt 的哈希都必须能校验原始明文")
	}
}

func TestValidatePasswordPolicy(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  error
	}{
		{"合法", "Abcd1234", nil},
		{"过短", "Abc123", ErrWeakPassword},
		{"纯字母", "abcdefgh", ErrWeakPassword},
		{"纯数字", "12345678", ErrWeakPassword},
		{"超长", "A1" + strings.Repeat("a", 200), ErrWeakPassword},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidatePasswordPolicy(tc.input)
			if !errors.Is(got, tc.want) {
				t.Fatalf("ValidatePasswordPolicy(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// ---------- JWT 签发 / 解析 / 过期 ----------

func TestJWT_IssueAndParse(t *testing.T) {
	m := NewManager("unit-test-secret", time.Hour)
	raw, exp, err := m.Issue(42, "alice", 7)
	if err != nil {
		t.Fatalf("Issue 失败: %v", err)
	}
	if raw == "" {
		t.Fatal("token 不应为空")
	}
	if d := time.Until(exp); d < 50*time.Minute || d > 70*time.Minute {
		t.Fatalf("过期时间不符合 TTL 预期: %v", d)
	}

	claims, err := m.Parse(raw)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if claims.UserID() != 42 {
		t.Fatalf("UserID = %d, want 42", claims.UserID())
	}
	if claims.Username != "alice" {
		t.Fatalf("Username = %q, want alice", claims.Username)
	}
	if claims.Ver != 7 {
		t.Fatalf("Ver = %d, want 7", claims.Ver)
	}
}

func TestJWT_Parse_TamperedSignatureRejected(t *testing.T) {
	m := NewManager("unit-test-secret", time.Hour)
	raw, _, err := m.Issue(1, "bob", 0)
	if err != nil {
		t.Fatalf("Issue 失败: %v", err)
	}
	// 篡改签名首字符，破坏签名。
	//
	// 注意：不能改末字符——base64url 末字符只承载 2 bit 有效数据、其余 4 bit 为填充，
	// 改末位有约 1/16 概率解码出完全相同的签名，会造成偶发假阴性。
	// 改首字符必然改变解码后的签名字节，断言更严格且稳定。
	parts := strings.SplitN(raw, ".", 3)
	if len(parts) != 3 || parts[2] == "" {
		t.Fatalf("token 结构异常: %v", raw)
	}
	sig := []byte(parts[2])
	if sig[0] == 'A' {
		sig[0] = 'B'
	} else {
		sig[0] = 'A'
	}
	tampered := parts[0] + "." + parts[1] + "." + string(sig)
	if _, err := m.Parse(tampered); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("篡改签名的 token 应被拒绝，实际 err=%v", err)
	}
}

func TestJWT_Parse_WrongKeyRejected(t *testing.T) {
	issuer := NewManager("secret-a", time.Hour)
	verifier := NewManager("secret-b", time.Hour)
	raw, _, err := issuer.Issue(1, "bob", 0)
	if err != nil {
		t.Fatalf("Issue 失败: %v", err)
	}
	if _, err := verifier.Parse(raw); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("不同密钥签发的 token 应被拒绝，实际 err=%v", err)
	}
}

func TestJWT_Parse_ExpiredRejected(t *testing.T) {
	// JWT 的 exp 以「秒」为精度序列化，因此 TTL 必须 >= 1s 才能构造出一个
	// 确定落在未来的过期时刻（<1s 会被截断到当前秒，立即过期）。
	m := NewManager("unit-test-secret", time.Second)
	raw, _, err := m.Issue(9, "carol", 0)
	if err != nil {
		t.Fatalf("Issue 失败: %v", err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := m.Parse(raw); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("过期 token 应被拒绝，实际 err=%v", err)
	}
}

func TestJWT_Parse_GarbageRejected(t *testing.T) {
	m := NewManager("unit-test-secret", time.Hour)
	for _, raw := range []string{"", "not-a-jwt", "a.b.c"} {
		if _, err := m.Parse(raw); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("非法 token %q 应被拒绝，实际 err=%v", raw, err)
		}
	}
}

// ---------- 中间件：token_version 不匹配必须拒绝 ----------

// fakeUsers 实现 UserLoader，返回固定用户（模拟库中记录）。
type fakeUsers struct {
	user *model.PlatformUser
	err  error
}

func (f *fakeUsers) GetByID(_ context.Context, id int64) (*model.PlatformUser, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.user == nil || f.user.ID != id {
		return nil, nil
	}
	return f.user, nil
}

// callAuthenticated 用给定 token 请求一次受保护端点，返回 HTTP 状态码与业务码。
func callAuthenticated(t *testing.T, mw *Middleware, token string) (int, int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/protected", mw.Authenticate(), func(c *gin.Context) {
		u := CurrentUser(c)
		if u == nil {
			t.Fatal("通过认证后 CurrentUser 不应为 nil")
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var body struct {
		Code int `json:"code"`
	}
	// 忽略解析错误：非 JSON 响应时 code 保持 0，由状态码断言兜底。
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body.Code
}

func TestAuthenticate_NoToken(t *testing.T) {
	m := NewManager("unit-test-secret", time.Hour)
	mw := NewMiddleware(m, &fakeUsers{})
	code, bizCode := callAuthenticated(t, mw, "")
	if code != http.StatusUnauthorized || bizCode != 40101 {
		t.Fatalf("无 token 应 401/40101，实际 %d/%d", code, bizCode)
	}
}

func TestAuthenticate_ValidToken(t *testing.T) {
	m := NewManager("unit-test-secret", time.Hour)
	u := &model.PlatformUser{ID: 5, Username: "dave", Role: RoleAdmin, Status: "active", TokenVersion: 3}
	raw, _, err := m.Issue(u.ID, u.Username, u.TokenVersion)
	if err != nil {
		t.Fatalf("Issue 失败: %v", err)
	}
	mw := NewMiddleware(m, &fakeUsers{user: u})
	code, bizCode := callAuthenticated(t, mw, raw)
	if code != http.StatusOK || bizCode != 0 {
		t.Fatalf("有效 token 应 200/0，实际 %d/%d", code, bizCode)
	}
}

func TestAuthenticate_TokenVersionMismatch(t *testing.T) {
	m := NewManager("unit-test-secret", time.Hour)
	// 签发时 ver=3，库中已被改密推进到 ver=4。
	u := &model.PlatformUser{ID: 5, Username: "dave", Role: RoleAdmin, Status: "active", TokenVersion: 4}
	raw, _, err := m.Issue(u.ID, u.Username, 3)
	if err != nil {
		t.Fatalf("Issue 失败: %v", err)
	}
	mw := NewMiddleware(m, &fakeUsers{user: u})
	code, bizCode := callAuthenticated(t, mw, raw)
	if code != http.StatusUnauthorized || bizCode != 40101 {
		t.Fatalf("token_version 不匹配应 401/40101（旧 token 立即失效），实际 %d/%d", code, bizCode)
	}
}

func TestAuthenticate_DisabledUser(t *testing.T) {
	m := NewManager("unit-test-secret", time.Hour)
	u := &model.PlatformUser{ID: 5, Username: "dave", Role: RoleAdmin, Status: "disabled", TokenVersion: 0}
	raw, _, _ := m.Issue(u.ID, u.Username, 0)
	mw := NewMiddleware(m, &fakeUsers{user: u})
	code, bizCode := callAuthenticated(t, mw, raw)
	if code != http.StatusUnauthorized || bizCode != 40101 {
		t.Fatalf("禁用账号应 401/40101，实际 %d/%d", code, bizCode)
	}
}

func TestAuthenticate_UserGone(t *testing.T) {
	m := NewManager("unit-test-secret", time.Hour)
	raw, _, _ := m.Issue(99, "ghost", 0)
	mw := NewMiddleware(m, &fakeUsers{})
	code, bizCode := callAuthenticated(t, mw, raw)
	if code != http.StatusUnauthorized || bizCode != 40101 {
		t.Fatalf("账号不存在应 401/40101，实际 %d/%d", code, bizCode)
	}
}

// ---------- RBAC 矩阵 ----------

func TestAllows_RoleMatrix(t *testing.T) {
	cases := []struct {
		role   string
		action Action
		want   bool
	}{
		{RoleAdmin, ActionClusterDelete, true},
		{RoleAdmin, ActionAuditView, true},
		{RoleOperator, ActionClusterTakeover, true},
		{RoleOperator, ActionInspectRun, true},
		{RoleOperator, ActionClusterDelete, true},
		{RoleOperator, ActionAuditView, false},
		{RoleOperator, ActionPlatformUserManage, false},
		{RoleViewer, ActionClusterView, true},
		{RoleViewer, ActionClusterTakeover, false},
		{RoleViewer, ActionClusterDelete, false},
		{RoleViewer, ActionInspectRun, false},
		{"unknown-role", ActionClusterView, false},
	}
	for _, tc := range cases {
		if got := Allows(tc.role, tc.action); got != tc.want {
			t.Fatalf("Allows(%q, %q) = %v, want %v", tc.role, tc.action, got, tc.want)
		}
	}
}

func TestRequire_ForbiddenBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin-only", func(c *gin.Context) {
		c.Set(CtxUserKey, &model.PlatformUser{ID: 2, Role: RoleViewer, Status: "active"})
	}, Require(ActionClusterDelete), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 0})
	})
	req := httptest.NewRequest(http.MethodGet, "/admin-only", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer 触发写操作应 403，实际 %d", w.Code)
	}
	var body struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.Code != 40301 {
		t.Fatalf("越权业务码应为 40301，实际 %d", body.Code)
	}
}
