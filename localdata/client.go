// Package localdata 는 공공데이터포털(data.go.kr)의
// 행정안전부 지방행정 인허가 API(예: general_restaurants)를 호출한다.
//
// 요청 형식 (공공데이터포털 명세 기준):
//
//	GET https://apis.data.go.kr/1741000/{service}/info
//	    ?serviceKey=...&pageNo=1&numOfRows=100&returnType=json
//	    &cond[LCPMT_YMD::GTE]=20261001      (인허가일자 이상)
//	    &cond[SALS_STTS_CD::EQ]=01           (영업상태: 01 영업/정상)
//	    &cond[OPN_ATMY_GRP_CD::EQ]=3000000   (개방자치단체코드)
//	    &cond[ROAD_NM_ADDR::LIKE]=마포구      (도로명주소 포함 검색)
package localdata

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const DefaultBaseURL = "https://apis.data.go.kr/1741000"

type Client struct {
	BaseURL    string
	ServiceKey string // 공공데이터포털의 "디코딩" 키를 넣는다 (url.Values 가 인코딩해 줌)
	HTTP       *http.Client
	PageSize   int
	// 호출 사이 최소 간격. 초당 호출 제한(에러코드 23)을 피하기 위함.
	MinInterval time.Duration
	// 인허가일자 파라미터의 날짜 형식. 명세가 바뀌면 여기만 고친다.
	DateLayout string

	last time.Time
}

func New(serviceKey string) *Client {
	return &Client{
		BaseURL:     DefaultBaseURL,
		ServiceKey:  serviceKey,
		HTTP:        &http.Client{Timeout: 30 * time.Second},
		PageSize:    100,
		MinInterval: 300 * time.Millisecond,
		DateLayout:  "20060102",
	}
}

// Query 는 한 업종(service)에 대한 조회 조건이다.
type Query struct {
	Service    string    // 예: "general_restaurants"
	PermitFrom time.Time // 인허가일자 >= (0 이면 조건 없음)
	PermitTo   time.Time // 인허가일자 <  (0 이면 조건 없음)
	Region     string    // 개방자치단체코드 (빈 값이면 전국)
	Status     string    // 영업상태코드 (기본 "01" 영업/정상, "-" 이면 조건 없음)
	AddrLike   string    // 도로명주소에 포함된 글자 (예: "서울특별시 마포구")
}

func (c *Client) values(q Query, page, rows int) url.Values {
	v := url.Values{}
	v.Set("serviceKey", c.ServiceKey)
	v.Set("pageNo", fmt.Sprint(page))
	v.Set("numOfRows", fmt.Sprint(rows))
	v.Set("returnType", "json")
	if !q.PermitFrom.IsZero() {
		v.Set("cond[LCPMT_YMD::GTE]", q.PermitFrom.Format(c.DateLayout))
	}
	if !q.PermitTo.IsZero() {
		v.Set("cond[LCPMT_YMD::LT]", q.PermitTo.Format(c.DateLayout))
	}
	switch q.Status {
	case "":
		v.Set("cond[SALS_STTS_CD::EQ]", "01")
	case "-":
	default:
		v.Set("cond[SALS_STTS_CD::EQ]", q.Status)
	}
	if q.Region != "" {
		v.Set("cond[OPN_ATMY_GRP_CD::EQ]", q.Region)
	}
	if q.AddrLike != "" {
		v.Set("cond[ROAD_NM_ADDR::LIKE]", q.AddrLike)
	}
	return v
}

// RawPage 는 한 페이지를 그대로 받아온다. 처음 연동할 때 실제 응답 모양을
// 눈으로 확인하는 용도(probe 명령)로 쓴다.
func (c *Client) RawPage(ctx context.Context, q Query, page, rows int) ([]byte, error) {
	if c.MinInterval > 0 {
		if wait := c.MinInterval - time.Since(c.last); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	c.last = time.Now()

	u := fmt.Sprintf("%s/%s/info?%s", c.BaseURL, q.Service, c.values(q, page, rows).Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet(body))
	}
	return body, nil
}

// FetchAll 은 조건에 맞는 모든 페이지를 받아 업소 목록으로 돌려준다.
func (c *Client) FetchAll(ctx context.Context, q Query) ([]Business, error) {
	var all []Business
	for page := 1; ; page++ {
		body, err := c.RawPage(ctx, q, page, c.PageSize)
		if err != nil {
			return all, fmt.Errorf("%s %d페이지: %w", q.Service, page, err)
		}
		items, total, err := ParsePage(body)
		if err != nil {
			return all, fmt.Errorf("%s %d페이지: %w", q.Service, page, err)
		}
		for i := range items {
			items[i].Service = q.Service
		}
		all = append(all, items...)

		if len(items) < c.PageSize || (total > 0 && len(all) >= total) {
			return all, nil
		}
		if page >= 1000 { // 안전장치
			return all, fmt.Errorf("%s: 페이지가 너무 많습니다(조건을 좁혀 주세요)", q.Service)
		}
	}
}

func snippet(b []byte) string {
	const max = 300
	s := string(b)
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
