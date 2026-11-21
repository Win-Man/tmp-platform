// Package inspect 巡检引擎：内置模板加载、判定函数注册表、报告渲染。
package inspect

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed templates/default.yaml
var defaultTemplateYAML []byte

// TemplateFile YAML 模板结构。
type TemplateFile struct {
	Code        string     `yaml:"code"`
	Name        string     `yaml:"name"`
	Description string     `yaml:"description"`
	Items       []ItemFile `yaml:"items"`
}

// ItemFile YAML 巡检项结构。
type ItemFile struct {
	Code        string `yaml:"code"`
	Name        string `yaml:"name"`
	Category    string `yaml:"category"`
	CheckMethod string `yaml:"checkMethod"`
	Severity    string `yaml:"severity"`
	Expr        string `yaml:"expr"`
	Expect      string `yaml:"expect"`
	Suggestion  string `yaml:"suggestion"`
	DocURL      string `yaml:"docUrl"`
	Applies     string `yaml:"applies"`
	SortOrder   int    `yaml:"sortOrder"`
}

// LoadDefaultTemplate 解析内嵌的默认全量模板。
func LoadDefaultTemplate() (*TemplateFile, error) {
	var tpl TemplateFile
	if err := yaml.Unmarshal(defaultTemplateYAML, &tpl); err != nil {
		return nil, fmt.Errorf("解析内嵌巡检模板失败: %w", err)
	}
	if tpl.Code == "" || len(tpl.Items) == 0 {
		return nil, fmt.Errorf("内嵌巡检模板内容为空")
	}
	// 校验：每项必须有判定表达式与修复建议（PRD F-51）。
	for _, it := range tpl.Items {
		if it.Code == "" || it.Name == "" || it.Category == "" {
			return nil, fmt.Errorf("巡检项缺少必填字段: %+v", it)
		}
		if it.Expr == "" {
			return nil, fmt.Errorf("巡检项 %s 缺少判定表达式", it.Code)
		}
		if it.Suggestion == "" {
			return nil, fmt.Errorf("巡检项 %s 缺少修复建议", it.Code)
		}
	}
	return &tpl, nil
}

// CategoryNames 分类展示名。
var CategoryNames = map[string]string{
	"system":      "系统层",
	"tidb_config": "TiDB 配置",
	"security":    "安全合规",
	"capacity":    "容量水位",
	"ha":          "高可用",
	"version":     "版本规范",
}

// CategoryOrder 分类展示顺序。
var CategoryOrder = []string{"system", "tidb_config", "security", "capacity", "ha", "version"}

// CategoryLabel 返回分类中文名，未知分类原样返回。
func CategoryLabel(c string) string {
	if v, ok := CategoryNames[c]; ok {
		return v
	}
	return c
}
