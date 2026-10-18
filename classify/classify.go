// Package classify 는 업소를 세부 업종으로 나누고 영업 포인트를 붙인다.
//
// ANTHROPIC_API_KEY 가 있으면 Claude 로 30곳씩 묶어서 한 번에 분류하고,
// 없거나 실패하면 상호명 키워드 규칙으로 분류한다.
package classify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/moolwave/meonjeo-gage/store"
)

var Categories = []string{
	"한식", "중식", "일식", "양식", "분식", "카페·디저트", "치킨·피자",
	"고기구이", "술집·주점", "베이커리", "배달전문", "기타",
}

// ---------- 규칙 기반 (API 키 없이도 동작) ----------

var rules = []struct {
	cat   string
	words []string
}{
	{"카페·디저트", []string{"카페", "커피", "cafe", "coffee", "디저트", "빙수", "티하우스"}},
	{"베이커리", []string{"베이커리", "제과", "빵", "bakery"}},
	{"치킨·피자", []string{"치킨", "통닭", "피자", "pizza"}},
	{"고기구이", []string{"고기", "갈비", "삼겹", "정육", "한우", "곱창", "막창", "숯불"}},
	{"술집·주점", []string{"포차", "호프", "주점", "이자카야", "술집", "펍", "pub", "bar", "와인"}},
	{"분식", []string{"분식", "김밥", "떡볶이", "라볶이", "순대"}},
	{"중식", []string{"반점", "중화", "짬뽕", "짜장", "마라", "훠궈", "양꼬치"}},
	{"일식", []string{"스시", "초밥", "라멘", "우동", "돈까스", "돈카츠", "사시미", "오마카세"}},
	{"양식", []string{"파스타", "스테이크", "브런치", "비스트로", "버거"}},
	{"한식", []string{"국밥", "해장", "백반", "냉면", "칼국수", "찌개", "한식", "보쌈", "족발", "닭갈비"}},
}

// 인허가 데이터의 업태구분명(예: "중국식") → 우리 분류
var bizTypeMap = map[string]string{
	"한식": "한식", "중국식": "중식", "일식": "일식", "경양식": "양식", "분식": "분식",
	"까페": "카페·디저트", "커피숍": "카페·디저트", "통닭(치킨)": "치킨·피자",
	"호프/통닭": "술집·주점", "정종/대포집/소주방": "술집·주점", "감성주점": "술집·주점",
	"패밀리레스트랑": "양식", "뷔페식": "기타", "외국음식전문점(인도,태국등)": "기타",
}

// ByRuleWith 은 상호명 키워드로 먼저 찾고, 없으면 인허가 업태구분명을 쓴다.
func ByRuleWith(name, bizType string) string {
	if c := ByRule(name); c != "기타" {
		return c
	}
	if c, ok := bizTypeMap[strings.TrimSpace(bizType)]; ok {
		return c
	}
	return "기타"
}

func ByRule(name string) string {
	n := strings.ToLower(name)
	for _, r := range rules {
		for _, w := range r.words {
			if strings.Contains(n, strings.ToLower(w)) {
				return r.cat
			}
		}
	}
	return "기타"
}

// ---------- Claude ----------

type Claude struct {
	APIKey   string
	Model    string // 예: "claude-haiku-5-5" (저렴하고 빠른 모델로 충분)
	Audience string // 리스트를 사는 사람. 예: "POS·카드단말기 영업사원"
	HTTP     *http.Client
	Endpoint string
}

func NewClaude(apiKey, model, audience string) *Claude {
	return &Claude{
		APIKey:   apiKey,
		Model:    model,
		Audience: audience,
		HTTP:     &http.Client{Timeout: 90 * time.Second},
		Endpoint: "https://api.anthropic.com/v1/messages",
	}
}

type result struct {
	ID       int    `json:"id"`
	Category string `json:"category"`
	Pitch    string `json:"pitch"`
}

func (c *Claude) prompt(batch []*store.Record) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `아래는 최근 인허가를 받은 신규 업소 목록입니다.
각 업소를 다음 분류 중 하나로 나누고, "%s"가 방문 영업할 때 쓸 수 있는
한 줄 영업 포인트(40자 이내, 상호명과 업종·주소에서 알 수 있는 사실만 근거로)를 써 주세요.
추측한 사실(매출, 대표자 정보 등)은 쓰지 마세요.

분류: %s

업소 목록:
`, c.Audience, strings.Join(Categories, ", "))
	for i, r := range batch {
		addr := r.RoadAddr
		if addr == "" {
			addr = r.LotAddr
		}
		fmt.Fprintf(&sb, "%d. 상호=%q, 인허가업종=%s, 업태=%s, 주소=%s\n", i, r.Name, r.Service, r.BizType, addr)
	}
	sb.WriteString(`
JSON 배열만 출력하세요. 형식: [{"id":0,"category":"한식","pitch":"..."}]`)
	return sb.String()
}

func (c *Claude) classifyBatch(ctx context.Context, batch []*store.Record) ([]result, error) {
	reqBody, _ := json.Marshal(map[string]any{
		"model":      c.Model,
		"max_tokens": 4000,
		"messages":   []map[string]string{{"role": "user", "content": c.prompt(batch)}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Claude API %d: %s", resp.StatusCode, body)
	}

	var msg struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	var text strings.Builder
	for _, b := range msg.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	s := text.String()
	start, end := strings.Index(s, "["), strings.LastIndex(s, "]")
	if start < 0 || end < start {
		return nil, fmt.Errorf("응답에서 JSON을 찾지 못함: %.200s", s)
	}
	var out []result
	if err := json.Unmarshal([]byte(s[start:end+1]), &out); err != nil {
		return nil, fmt.Errorf("JSON 해석 실패: %w", err)
	}
	return out, nil
}

func valid(cat string) bool {
	for _, c := range Categories {
		if c == cat {
			return true
		}
	}
	return false
}

// Run 은 레코드들을 분류한다. claude 가 nil 이면 규칙만 쓴다.
// 돌려주는 값: AI로 분류한 수, 규칙으로 분류한 수.
func Run(ctx context.Context, claude *Claude, recs []*store.Record, logf func(string, ...any)) (ai, rule int) {
	const batchSize = 30
	for i := 0; i < len(recs); i += batchSize {
		batch := recs[i:min(i+batchSize, len(recs))]
		done := make([]bool, len(batch))

		if claude != nil {
			res, err := claude.classifyBatch(ctx, batch)
			if err != nil {
				logf("AI 분류 실패, 규칙으로 대체: %v", err)
			}
			for _, r := range res {
				if r.ID < 0 || r.ID >= len(batch) || !valid(r.Category) {
					continue
				}
				batch[r.ID].Category = r.Category
				batch[r.ID].Pitch = strings.TrimSpace(r.Pitch)
				batch[r.ID].ClassifiedBy = "ai"
				done[r.ID] = true
				ai++
			}
		}
		for j, r := range batch {
			if !done[j] {
				r.Category = ByRuleWith(r.Name, r.BizType)
				r.ClassifiedBy = "rule"
				rule++
			}
		}
	}
	return ai, rule
}
