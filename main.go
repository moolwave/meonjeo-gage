// 먼저가게: 이번 주 새로 문 연 가게, 경쟁사보다 먼저.
// 신규 개업 업소 영업 리드 수집기
//
//	meonjeo probe    API 응답을 그대로 출력 (처음 연동할 때 확인용)
//	meonjeo collect  최근 인허가 업소를 받아 data/leads.json 에 쌓기
//	meonjeo classify 아직 분류 안 된 업소를 AI(또는 규칙)로 분류
//	meonjeo export   최근 N일 개업 업소를 엑셀로 내보내기
//	meonjeo run      collect → classify → export 한 번에
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/moolwave/meonjeo-gage/classify"
	"github.com/moolwave/meonjeo-gage/export"
	"github.com/moolwave/meonjeo-gage/localdata"
	"github.com/moolwave/meonjeo-gage/store"
)

var serviceLabels = map[string]string{
	"general_restaurants": "일반음식점",
	"printing_shops":      "인쇄사",
}

type opts struct {
	services  []string
	region    string
	addr      string
	inclTemp  bool
	days      int
	dbPath    string
	outPath   string
	model     string
	audience  string
	noAI      bool
	baseURL   string
	dateFmt   string
	dataKey   string
	claudeKey string
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	var o opts
	services := fs.String("services", "general_restaurants", "업종 서비스명 (쉼표로 여러 개)")
	fs.StringVar(&o.region, "region", "", "개방자치단체코드 (비우면 전국)")
	fs.StringVar(&o.addr, "addr", "", "도로명주소에 포함된 지역명 (예: \"서울특별시 마포구\")")
	fs.BoolVar(&o.inclTemp, "include-temp", false, "축제·행사장 등 임시 업소도 엑셀에 포함")
	fs.IntVar(&o.days, "days", 7, "최근 며칠 동안 인허가된 업소")
	fs.StringVar(&o.dbPath, "db", "data/leads.json", "저장 파일")
	fs.StringVar(&o.outPath, "out", "", "엑셀 파일 경로 (기본: 먼저가게_YYYYMMDD.xlsx)")
	fs.StringVar(&o.model, "model", envOr("CLAUDE_MODEL", "claude-haiku-5-5"), "분류에 쓸 Claude 모델")
	fs.StringVar(&o.audience, "audience", "POS·카드단말기·세무기장 등 개업 필수 서비스 영업사원", "리스트를 쓸 영업 대상")
	fs.BoolVar(&o.noAI, "no-ai", false, "AI 없이 규칙으로만 분류")
	fs.StringVar(&o.baseURL, "base-url", localdata.DefaultBaseURL, "API 주소 (테스트용)")
	fs.StringVar(&o.dateFmt, "date-format", "20060102", "인허가일자 파라미터 형식 (Go 날짜 형식)")
	fs.Parse(os.Args[2:])

	o.services = splitList(*services)
	if len(o.services) == 0 {
		fmt.Fprintln(os.Stderr, "-services 에 업종 서비스명을 하나 이상 넣어 주세요")
		os.Exit(2)
	}
	o.dataKey = os.Getenv("DATA_GO_KR_KEY")
	o.claudeKey = os.Getenv("ANTHROPIC_API_KEY")
	if o.outPath == "" {
		o.outPath = "먼저가게_" + time.Now().Format("20060102") + ".xlsx"
	}

	ctx := context.Background()
	var err error
	switch cmd {
	case "probe":
		err = probe(ctx, o)
	case "collect":
		err = withStore(o, func(s *store.Store) error { return collect(ctx, o, s) })
	case "classify":
		err = withStore(o, func(s *store.Store) error { return classifyCmd(ctx, o, s) })
	case "export":
		err = withStore(o, func(s *store.Store) error { return exportCmd(o, s) })
	case "run":
		err = withStore(o, func(s *store.Store) error {
			if err := collect(ctx, o, s); err != nil {
				return err
			}
			if err := s.Save(); err != nil { // 분류 중 실패해도 수집분은 남긴다
				return err
			}
			if err := classifyCmd(ctx, o, s); err != nil {
				return err
			}
			return exportCmd(o, s)
		})
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `사용법: meonjeo <probe|collect|classify|export|run> [옵션]

환경변수:
  DATA_GO_KR_KEY     공공데이터포털 인증키 (디코딩 키)
  ANTHROPIC_API_KEY  Claude API 키 (없으면 규칙 분류)

예:
  meonjeo probe   -services general_restaurants -days 3
  meonjeo run     -services general_restaurants -addr "서울특별시 마포구" -days 7
  meonjeo run     -services general_restaurants -region 3130000 -days 7`)
}

func withStore(o opts, fn func(*store.Store) error) error {
	s, err := store.Open(o.dbPath)
	if err != nil {
		return fmt.Errorf("저장 파일 열기: %w", err)
	}
	if err := fn(s); err != nil {
		return err
	}
	return s.Save()
}

func newClient(o opts) (*localdata.Client, error) {
	if o.dataKey == "" {
		return nil, fmt.Errorf("DATA_GO_KR_KEY 환경변수를 설정해 주세요 (공공데이터포털 '디코딩' 인증키)")
	}
	c := localdata.New(o.dataKey)
	c.BaseURL = strings.TrimRight(o.baseURL, "/")
	c.DateLayout = o.dateFmt
	return c, nil
}

func query(o opts, service string) localdata.Query {
	y, m, d := time.Now().Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	return localdata.Query{
		Service:    service,
		PermitFrom: today.AddDate(0, 0, -o.days),
		Region:     o.region,
		AddrLike:   o.addr,
	}
}

func probe(ctx context.Context, o opts) error {
	c, err := newClient(o)
	if err != nil {
		return err
	}
	q := query(o, o.services[0])
	body, err := c.RawPage(ctx, q, 1, 3)
	if err != nil {
		return err
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, body, "", "  ") == nil {
		body = pretty.Bytes()
	}
	fmt.Println(string(body))

	items, total, err := localdata.ParsePage(body)
	if err != nil {
		return fmt.Errorf("파싱: %w", err)
	}
	fmt.Fprintf(os.Stderr, "\n해석 결과: 전체 %d건 중 %d건 읽음\n", total, len(items))
	for _, b := range items {
		fmt.Fprintf(os.Stderr, "  - %s | %s | 인허가 %s | 상태 %s %s | 관리번호 %s\n",
			b.Name, firstNonEmpty(b.RoadAddr, b.LotAddr), b.PermitDate, b.Status, b.StatusName, b.MgmtNo)
	}
	if len(items) > 0 && items[0].Name == "" {
		fmt.Fprintln(os.Stderr, "주의: 사업장명을 못 읽었습니다. localdata/parse.go 의 aliases 를 응답 필드명에 맞춰 주세요.")
	}
	return nil
}

func collect(ctx context.Context, o opts, s *store.Store) error {
	c, err := newClient(o)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, svc := range o.services {
		list, err := c.FetchAll(ctx, query(o, svc))
		if err != nil {
			return err
		}
		added := 0
		for _, b := range list {
			if s.Upsert(b, now) {
				added++
			}
		}
		fmt.Printf("[수집] %s: %d건 조회, 신규 %d건\n", label(svc), len(list), added)
	}
	fmt.Printf("[수집] 누적 %d건\n", s.Len())
	return nil
}

func classifyCmd(ctx context.Context, o opts, s *store.Store) error {
	recs := s.Unclassified(0)
	if len(recs) == 0 {
		fmt.Println("[분류] 새로 분류할 업소 없음")
		return nil
	}
	var claude *classify.Claude
	if !o.noAI && o.claudeKey != "" {
		claude = classify.NewClaude(o.claudeKey, o.model, o.audience)
	}
	logf := func(f string, a ...any) { fmt.Fprintf(os.Stderr, "[분류] "+f+"\n", a...) }
	ai, rule := classify.Run(ctx, claude, recs, logf)
	fmt.Printf("[분류] AI %d건, 규칙 %d건\n", ai, rule)
	return nil
}

func exportCmd(o opts, s *store.Store) error {
	since := time.Now().AddDate(0, 0, -o.days).Format("20060102")
	col := func(t string, w float64) export.Column { return export.Column{Title: t, Width: w} }
	cols := []export.Column{
		col("인허가일", 12), col("인허가업종", 12), col("세부분류", 12), col("상호", 28),
		col("도로명주소", 45), col("지번주소", 40), col("영업상태", 10), col("영업 포인트", 45),
		col("면적(㎡)", 10), col("비고", 14),
		col("자치단체코드", 12), col("관리번호", 22), col("수집일", 12),
	}
	var rows [][]string
	skippedTemp := 0
	for _, r := range s.All() {
		d := digits(r.PermitDate)
		if len(d) >= 8 && d[:8] < since {
			continue
		}
		if o.region != "" && r.RegionCode != "" && r.RegionCode != o.region {
			continue
		}
		if o.addr != "" && !strings.Contains(r.RoadAddr+" "+r.LotAddr, o.addr) {
			continue
		}
		note := ""
		if isTemporary(r.Name, r.RoadAddr, r.LotAddr) {
			if !o.inclTemp {
				skippedTemp++
				continue
			}
			note = "임시·행사 업소 의심"
		}
		category := r.Category
		if category == "" {
			category = r.BizType
		}
		rows = append(rows, []string{
			prettyDate(d), label(r.Service), category, r.Name,
			r.RoadAddr, r.LotAddr, statusText(r.Status, r.StatusName), r.Pitch,
			r.Area, note, r.RegionCode, r.MgmtNo, r.FirstSeen.Format("2006-01-02"),
		})
	}
	if err := export.WriteXLSX(o.outPath, "신규개업", cols, rows); err != nil {
		return err
	}
	fmt.Printf("[내보내기] %s (%d건, 최근 %d일)\n", o.outPath, len(rows), o.days)
	if skippedTemp > 0 {
		fmt.Printf("[내보내기] 축제·행사장 등 임시 업소 %d건 제외 (-include-temp 로 포함 가능)\n", skippedTemp)
	}
	return nil
}

// ---------- 작은 도우미 ----------

// 축제·행사장 부스처럼 며칠만 영업하는 업소는 영업 가치가 낮다.
var tempWords = []string{"축제", "행사장", "박람회", "페스티벌", "엑스포", "야시장", "임시", "플리마켓"}

func isTemporary(fields ...string) bool {
	all := strings.Join(fields, " ")
	for _, w := range tempWords {
		if strings.Contains(all, w) {
			return true
		}
	}
	return false
}

func label(svc string) string {
	if l, ok := serviceLabels[svc]; ok {
		return l
	}
	return svc
}

func statusText(code, name string) string {
	if name != "" {
		return name
	}
	switch code {
	case "01":
		return "영업/정상"
	case "02":
		return "휴업"
	case "03":
		return "폐업"
	case "04":
		return "취소/말소"
	}
	return code
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func prettyDate(d string) string {
	if len(d) >= 8 {
		return d[:4] + "-" + d[4:6] + "-" + d[6:8]
	}
	return d
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
