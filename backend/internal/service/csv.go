package service

import (
	"bytes"
	"encoding/csv"
	"fmt"
)

// csvEscape 处理 Excel 公式注入风险（以 = + - @ 开头的单元格前置单引号）。
func csvEscape(v string) string {
	if v == "" {
		return ""
	}
	switch v[0] {
	case '=', '+', '-', '@':
		return "'" + v
	}
	return v
}

// buildCSV 生成 CSV 字节流（带 UTF-8 BOM，便于 Excel 正确识别中文）。
func buildCSV(header []string, rows [][]string) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF")
	w := csv.NewWriter(&buf)
	if err := w.Write(header); err != nil {
		return nil, err
	}
	for _, row := range rows {
		escaped := make([]string, len(row))
		for i, cell := range row {
			escaped[i] = csvEscape(cell)
		}
		if err := w.Write(escaped); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// csvFilename 生成安全的 CSV 文件名（去掉路径分隔符）。
func csvFilename(base string) string {
	out := make([]rune, 0, len(base)+4)
	for _, r := range base {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', ' ':
			out = append(out, '_')
		default:
			out = append(out, r)
		}
	}
	return fmt.Sprintf("%s.csv", string(out))
}
