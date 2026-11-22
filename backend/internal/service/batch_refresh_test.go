package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/tmp-platform/backend/internal/pkg/apperr"
)

// TestBatchRefresh_KeepsFailureReason 验证批量提交时单个集群失败不拖垮整批，
// 并且失败项带上可读原因（jobId=0 + error）。
//
// 背景：前端曾按「返回裸数组」解析批量结果，实际服务端返回 { results: [...] }，
// 导致提示文案出现「已提交 undefined 个采集任务」；同时旧实现把失败项写成
// jobId=0 却不给原因，前端无法区分「没提交」和「提交成功」。
//
// 入参用两个不存在的集群：单元素入参下 len(items) == len(ids) 天然成立，
// 等于没断言，「循环里漏 append 一条」这类 bug 会漏网。
func TestBatchRefresh_KeepsFailureReason(t *testing.T) {
	svc := identTestService(t)
	ctx := context.Background()
	ids := []int64{987654, 987655}

	items, err := svc.BatchRefresh(ctx, ids, nil)
	if err != nil {
		t.Fatalf("批量刷新不应整体报错: %v", err)
	}
	if len(items) != len(ids) {
		t.Fatalf("结果条数应为 %d（每个入参都要回填一条），实际 %d", len(ids), len(items))
	}
	for i, got := range items {
		wantID := ids[i]
		if got.ClusterID != wantID {
			t.Fatalf("第 %d 条 clusterId 应为 %d，实际 %d", i, wantID, got.ClusterID)
		}
		if got.JobID != 0 {
			t.Fatalf("集群 %d 不存在，不应产生任务，实际 jobId=%d", wantID, got.JobID)
		}
		if got.Error != "集群不存在" {
			t.Fatalf("集群 %d 的失败原因应为「集群不存在」，实际 %q", wantID, got.Error)
		}
	}
}

// TestBatchRefresh_RejectsEmptySelection 验证空选择直接报参数错误。
func TestBatchRefresh_RejectsEmptySelection(t *testing.T) {
	svc := identTestService(t)
	_, err := svc.BatchRefresh(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("空选择应返回 40001 参数错误")
	}
	if ae := apperr.From(err); ae == nil || ae.Code != apperr.CodeBadRequest {
		t.Fatalf("空选择应返回 40001，实际 %v", err)
	}
}

// TestBatchRefresh_RejectsTooManySelection 验证超过 50 个集群时拒绝，避免一次点击压垮任务引擎。
func TestBatchRefresh_RejectsTooManySelection(t *testing.T) {
	svc := identTestService(t)
	ids := make([]int64, 0, 51)
	for i := 0; i < 51; i++ {
		ids = append(ids, int64(i+1))
	}
	_, err := svc.BatchRefresh(context.Background(), ids, nil)
	if err == nil {
		t.Fatal("超过 50 个集群应返回 40001 参数错误")
	}
	if ae := apperr.From(err); ae == nil || ae.Code != apperr.CodeBadRequest {
		t.Fatalf("超过 50 个集群应返回 40001，实际 %v", err)
	}
}

// TestFailureReason 验证失败原因的归一规则：
//
//   - 非 apperr 错误（如 engine/驱动返回的裸错误）只回固定文案，绝不外泄原文，
//     否则 DB 地址、端口、账号之类细节会经 toast 漏给前端；
//   - apperr 错误透出其 Message（不带 "40401: " 这类错误码前缀）；
//   - 被 wrap 过的 apperr 仍能穿透取到 Message，避免中间层加一层 %w 就退化。
func TestFailureReason(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "裸错误收敛为固定文案",
			err:  errors.New("dial tcp 10.0.0.1:4000: connection refused"),
			want: "提交失败",
		},
		{
			name: "apperr 透出 Message",
			err:  apperr.NotFound("集群不存在"),
			want: "集群不存在",
		},
		{
			name: "wrap 后的 apperr 仍可取到 Message",
			err:  fmt.Errorf("wrap: %w", apperr.TooMany("任务队列已满，请稍后重试")),
			want: "任务队列已满，请稍后重试",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := failureReason(c.err); got != c.want {
				t.Fatalf("failureReason(%v) = %q，应为 %q", c.err, got, c.want)
			}
		})
	}
}

// TestRefreshResult_WireShape 固定批量结果的 JSON 形状：
//
//   - 成功项不带 error 字段（omitempty），只靠 jobId 判断「已提交」；
//   - 失败项 jobId 为 0 且必须带 error。
//
// 前端 ClusterListView.batchRefresh 正是按这两个约定分区渲染提示文案，
// 改动字段名或去掉 omitempty 会让提示退化成「已提交 0 个」这类误导文案。
func TestRefreshResult_WireShape(t *testing.T) {
	okRaw, err := json.Marshal(RefreshResult{ClusterID: 1, JobID: 42, Reused: true})
	if err != nil {
		t.Fatalf("序列化成功项失败: %v", err)
	}
	var okMap map[string]any
	if err := json.Unmarshal(okRaw, &okMap); err != nil {
		t.Fatalf("反序列化成功项失败: %v", err)
	}
	for _, key := range []string{"clusterId", "jobId", "reused"} {
		if _, hit := okMap[key]; !hit {
			t.Fatalf("成功项缺少字段 %q，实际 JSON=%s", key, okRaw)
		}
	}
	if _, hit := okMap["error"]; hit {
		t.Fatalf("成功项不应输出 error 字段，实际 JSON=%s", okRaw)
	}

	failRaw, err := json.Marshal(RefreshResult{ClusterID: 2, Error: "集群不存在"})
	if err != nil {
		t.Fatalf("序列化失败项失败: %v", err)
	}
	var failMap map[string]any
	if err := json.Unmarshal(failRaw, &failMap); err != nil {
		t.Fatalf("反序列化失败项失败: %v", err)
	}
	if got, _ := failMap["error"].(string); got != "集群不存在" {
		t.Fatalf("失败项 error 字段应为「集群不存在」，实际 JSON=%s", failRaw)
	}
	if jobID, _ := failMap["jobId"].(float64); jobID != 0 {
		t.Fatalf("失败项 jobId 应为 0，实际 JSON=%s", failRaw)
	}
}
