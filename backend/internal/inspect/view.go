package inspect

// ItemView 巡检项对外视图（模板详情 / 项库使用）。
type ItemView struct {
	ID            int64  `json:"id"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	CategoryLabel string `json:"categoryLabel"`
	CheckMethod   string `json:"checkMethod"`
	Severity      string `json:"severity"`
	Expr          string `json:"expr"`
	Expect        string `json:"expect"`
	Suggestion    string `json:"suggestion"`
	DocURL        string `json:"docUrl"`
	Applies       string `json:"applies"`
	SortOrder     int    `json:"sortOrder"`
}
