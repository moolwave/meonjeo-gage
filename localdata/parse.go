package localdata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Business 는 인허가 업소 한 곳이다. 필드 이름은 API 버전마다 조금씩 달라서
// (예: BPLC_NM / bplcNm), 파싱할 때 대소문자·밑줄을 무시하고 맞춘다.
type Business struct {
	Service    string          `json:"service"`
	MgmtNo     string          `json:"mgmtNo"`     // 관리번호 (자치단체 안에서 고유)
	RegionCode string          `json:"regionCode"` // 개방자치단체코드
	Name       string          `json:"name"`       // 사업장명
	RoadAddr   string          `json:"roadAddr"`   // 도로명주소
	LotAddr    string          `json:"lotAddr"`    // 지번주소
	PermitDate string          `json:"permitDate"` // 인허가일자
	Status     string          `json:"status"`     // 영업상태코드
	StatusName string          `json:"statusName"` // 영업상태명
	BizType    string          `json:"bizType"`    // 업태구분명 (예: 한식, 기타)
	Area       string          `json:"area"`       // 소재지면적(㎡)
	UpdatedAt  string          `json:"updatedAt"`  // 데이터갱신시점
	Raw        json.RawMessage `json:"raw,omitempty"`
}

// Key 는 업소를 중복 없이 식별하는 값이다.
func (b Business) Key() string {
	id := b.MgmtNo
	if id == "" { // 관리번호가 없으면 이름+주소로 대신한다
		id = b.Name + "|" + b.RoadAddr + b.LotAddr
	}
	return b.Service + "|" + b.RegionCode + "|" + id
}

// 정규화된 키(소문자, 밑줄 제거) → 후보 이름들. 앞쪽이 신규 data.go.kr 명세.
var aliases = map[string][]string{
	"name":       {"bplcnm"},
	"road":       {"roadnmaddr", "rdnwhladdr"},
	"lot":        {"lotnoaddr", "sitewhladdr"},
	"permit":     {"lcpmtymd", "apvpermymd"},
	"status":     {"salssttscd", "trdstategbn"},
	"statusName": {"salssttsnm", "trdstatenm"},
	"bizType":    {"bzstatsenm", "snttnbzstatnm", "uptaenm", "bztpnm", "bizcndnm"},
	"area":       {"lctnarea", "fcltotalscl", "sitearea"},
	"mgmt":       {"mngno", "mgtno"},
	"region":     {"opnatmygrpcd", "opnsfteamcode"},
	"updated":    {"datupdtpnt", "lastmodts"},
}

func norm(k string) string {
	return strings.ToLower(strings.ReplaceAll(k, "_", ""))
}

// ParsePage 는 응답 본문에서 업소 목록과 전체 건수를 꺼낸다.
// 응답 구조(response.body.items.item 등)가 조금 달라도 동작하도록
// "업소처럼 생긴 객체의 배열"을 재귀적으로 찾는다.
func ParsePage(body []byte) ([]Business, int, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		// 게이트웨이 오류는 XML/텍스트로 오는 경우가 많다.
		return nil, 0, fmt.Errorf("JSON이 아닌 응답: %s", snippet(trimmed))
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, 0, fmt.Errorf("JSON 해석 실패: %w", err)
	}

	if code, msg := findResult(root); code != "" && code != "00" && code != "0" && code != "INFO-000" {
		if code == "03" || strings.Contains(strings.ToUpper(msg), "NODATA") {
			return nil, 0, nil // 데이터 없음은 오류가 아니다
		}
		return nil, 0, fmt.Errorf("API 오류 %s: %s", code, msg)
	}

	total := findInt(root, "totalcount")
	rows := findItems(root)
	out := make([]Business, 0, len(rows))
	for _, m := range rows {
		out = append(out, toBusiness(m))
	}
	return out, total, nil
}

func toBusiness(m map[string]any) Business {
	idx := make(map[string]any, len(m))
	for k, v := range m {
		idx[norm(k)] = v
	}
	get := func(field string) string {
		for _, k := range aliases[field] {
			if v, ok := idx[k]; ok && v != nil {
				return strings.TrimSpace(fmt.Sprint(v))
			}
		}
		return ""
	}
	raw, _ := json.Marshal(m)
	return Business{
		MgmtNo:     get("mgmt"),
		RegionCode: get("region"),
		Name:       get("name"),
		RoadAddr:   get("road"),
		LotAddr:    get("lot"),
		PermitDate: get("permit"),
		Status:     get("status"),
		StatusName: get("statusName"),
		BizType:    get("bizType"),
		Area:       get("area"),
		UpdatedAt:  get("updated"),
		Raw:        raw,
	}
}

// isBusiness: 사업장명 키가 있으면 업소 객체로 본다.
func isBusiness(m map[string]any) bool {
	for k := range m {
		for _, a := range aliases["name"] {
			if norm(k) == a {
				return true
			}
		}
	}
	return false
}

func findItems(v any) []map[string]any {
	switch t := v.(type) {
	case map[string]any:
		if isBusiness(t) { // item 이 배열이 아니라 객체 하나로 오는 경우
			return []map[string]any{t}
		}
		for _, child := range t {
			if found := findItems(child); found != nil {
				return found
			}
		}
	case []any:
		var rows []map[string]any
		for _, e := range t {
			if m, ok := e.(map[string]any); ok && isBusiness(m) {
				rows = append(rows, m)
			}
		}
		if len(rows) > 0 {
			return rows
		}
		for _, e := range t {
			if found := findItems(e); found != nil {
				return found
			}
		}
	}
	return nil
}

func findInt(v any, key string) int {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if norm(k) == key {
				if n, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(child))); err == nil {
					return n
				}
			}
		}
		for _, child := range t {
			if n := findInt(child, key); n != 0 {
				return n
			}
		}
	case []any:
		for _, e := range t {
			if n := findInt(e, key); n != 0 {
				return n
			}
		}
	}
	return 0
}

func findResult(v any) (code, msg string) {
	m, ok := v.(map[string]any)
	if !ok {
		return "", ""
	}
	for k, child := range m {
		switch norm(k) {
		case "resultcode":
			code = strings.TrimSpace(fmt.Sprint(child))
		case "resultmsg":
			msg = strings.TrimSpace(fmt.Sprint(child))
		}
	}
	if code != "" {
		return code, msg
	}
	for k, child := range m {
		if strings.Contains(norm(k), "item") { // 업소 목록 안까지 뒤지지 않는다
			continue
		}
		if c, mm := findResult(child); c != "" {
			return c, mm
		}
	}
	return "", ""
}
