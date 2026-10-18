package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moolwave/meonjeo-gage/classify"
	"github.com/moolwave/meonjeo-gage/store"
)

// 가짜 data.go.kr: 250건을 페이지로 나눠서 돌려준다.
func fakeDataAPI(t *testing.T) *httptest.Server {
	names := []string{"골목커피", "왕십리곱창", "행복반점", "새벽포차", "정다운김밥"}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/general_restaurants/info" {
			t.Errorf("경로: %s", r.URL.Path)
		}
		if q.Get("serviceKey") != "test+key/=" { // 디코딩 키가 한 번만 인코딩됐는지
			t.Errorf("serviceKey: %q", q.Get("serviceKey"))
		}
		if q.Get("cond[SALS_STTS_CD::EQ]") != "01" || q.Get("cond[LCPMT_YMD::GTE]") == "" || q.Get("returnType") != "json" {
			t.Errorf("조건 파라미터: %v", q)
		}
		page, _ := strconv.Atoi(q.Get("pageNo"))
		rows, _ := strconv.Atoi(q.Get("numOfRows"))
		const total = 250
		var items []map[string]string
		for i := (page - 1) * rows; i < min(page*rows, total); i++ {
			items = append(items, map[string]string{
				"MNG_NO":          fmt.Sprintf("M-%04d", i),
				"OPN_ATMY_GRP_CD": "3000000",
				"BPLC_NM":         fmt.Sprintf("%s %d호점", names[i%len(names)], i),
				"ROAD_NM_ADDR":    fmt.Sprintf("서울특별시 종로구 예시로 %d", i),
				"LCPMT_YMD":       time.Now().AddDate(0, 0, -(i % 5)).Format("20060102"),
				"SALS_STTS_CD":    "01",
				"SALS_STTS_NM":    "영업/정상",
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{
			"header": map[string]string{"resultCode": "00", "resultMsg": "NORMAL SERVICE"},
			"body":   map[string]any{"totalCount": total, "items": map[string]any{"item": items}},
		}})
	}))
}

// 가짜 Claude: 첫 번째 업소만 빼고 모두 "한식"으로 분류해서, 규칙 대체도 검증한다.
func fakeClaude(t *testing.T, calls *int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		if r.Header.Get("x-api-key") != "sk-test" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("헤더 누락")
		}
		var req struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &req)
		n := strings.Count(req.Messages[0].Content, "상호=")
		var res []map[string]any
		for i := 1; i < n; i++ {
			res = append(res, map[string]any{"id": i, "category": "한식", "pitch": "개업 첫 달 카드단말기 수요"})
		}
		out, _ := json.Marshal(res)
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": "결과:\n" + string(out)}},
		})
	}))
}

func TestEndToEnd(t *testing.T) {
	api := fakeDataAPI(t)
	defer api.Close()
	calls := 0
	ai := fakeClaude(t, &calls)
	defer ai.Close()

	dir := t.TempDir()
	o := opts{
		services: []string{"general_restaurants"},
		days:     7,
		dbPath:   filepath.Join(dir, "leads.json"),
		outPath:  envOr("MEONJEO_SAMPLE_OUT", filepath.Join(dir, "out.xlsx")),
		baseURL:  api.URL,
		dateFmt:  "20060102",
		dataKey:  "test+key/=",
	}
	ctx := context.Background()

	s, _ := store.Open(o.dbPath)
	if err := collect(ctx, o, s); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 250 {
		t.Fatalf("수집 %d건, 250건이어야 함", s.Len())
	}
	// 같은 기간을 다시 수집해도 중복되지 않아야 한다
	if err := collect(ctx, o, s); err != nil || s.Len() != 250 {
		t.Fatalf("재수집 후 %d건 (%v)", s.Len(), err)
	}

	c := classify.NewClaude("sk-test", "claude-haiku-5-5", "영업사원")
	c.Endpoint = ai.URL
	aiN, ruleN := classify.Run(ctx, c, s.Unclassified(0), t.Logf)
	if calls != 9 || aiN+ruleN != 250 || ruleN != 9 {
		t.Fatalf("호출 %d회, AI %d, 규칙 %d", calls, aiN, ruleN)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	// 저장 파일을 다시 열어도 분류가 남아 있어야 한다
	s2, _ := store.Open(o.dbPath)
	if len(s2.Unclassified(0)) != 0 {
		t.Fatal("다시 열었더니 미분류 업소가 있음")
	}
	if err := exportCmd(o, s2); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(o.outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == "xl/worksheets/sheet1.xml" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			if got := strings.Count(string(b), "<row "); got != 251 {
				t.Fatalf("엑셀 행 %d개, 251개(머리글 포함)여야 함", got)
			}
			return
		}
	}
	t.Fatal("시트가 없음")
}

func TestRuleClassifier(t *testing.T) {
	cases := map[string]string{
		"골목커피": "카페·디저트", "왕십리곱창": "고기구이", "행복반점": "중식",
		"새벽포차": "술집·주점", "정다운김밥": "분식", "홍길동상회": "기타",
	}
	for name, want := range cases {
		if got := classify.ByRule(name); got != want {
			t.Errorf("%s → %s, 기대값 %s", name, got, want)
		}
	}
}

func TestTemporaryAndBizType(t *testing.T) {
	if !isTemporary("용연장터", "", "제주특별자치도 제주시 용담이동 461 외 5필지 2026용담용연음악회문화제 축제장내") {
		t.Error("축제장 업소를 임시로 인식해야 함")
	}
	if isTemporary("금등갈비", "제주특별자치도 서귀포시 일주동로 8720, 1층 (서홍동)") {
		t.Error("일반 업소를 임시로 보면 안 됨")
	}
	if got := classify.ByRuleWith("용연장터", "한식"); got != "한식" {
		t.Errorf("업태구분 대체: %s", got)
	}
	if got := classify.ByRuleWith("금등갈비", "한식"); got != "고기구이" {
		t.Errorf("상호 키워드 우선: %s", got)
	}
}
