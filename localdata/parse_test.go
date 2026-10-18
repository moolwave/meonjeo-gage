package localdata

import (
	"os"
	"reflect"
	"testing"
)

func TestParseNewStyle(t *testing.T) {
	body := `{"response":{"header":{"resultCode":"00","resultMsg":"NORMAL SERVICE"},
	"body":{"totalCount":2,"pageNo":1,"numOfRows":10,"items":{"item":[
	 {"MNG_NO":"3000000-101-2026-00001","OPN_ATMY_GRP_CD":"3000000","BPLC_NM":"골목커피","ROAD_NM_ADDR":"서울특별시 종로구 예시로 1","LCPMT_YMD":"20261012","SALS_STTS_CD":"01","SALS_STTS_NM":"영업/정상","UPTAE_NM":"까페"},
	 {"MNG_NO":"3000000-101-2026-00002","OPN_ATMY_GRP_CD":"3000000","BPLC_NM":"할매국밥","ROAD_NM_ADDR":"서울특별시 종로구 예시로 2","LCPMT_YMD":"2026-10-13","SALS_STTS_CD":"01"}
	]}}}}`
	items, total, err := ParsePage([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("total=%d len=%d", total, len(items))
	}
	b := items[0]
	if b.Name != "골목커피" || b.MgmtNo == "" || b.RegionCode != "3000000" || b.PermitDate != "20261012" || b.BizType != "까페" {
		t.Fatalf("bad parse: %+v", b)
	}
}

// 예전 LOCALDATA 형식(camelCase 키)과 item 이 객체 하나인 경우
func TestParseLegacySingleItem(t *testing.T) {
	body := `{"result":{"header":{"process":{"code":"00"}},"body":{"rows":[{"row":
	 {"mgtNo":"A-1","opnSfTeamCode":"3000000","bplcNm":"동네분식","rdnWhlAddr":"서울 어딘가 3","apvPermYmd":"20261010","trdStateGbn":"01"}}]}}}`
	items, _, err := ParsePage([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "동네분식" || items[0].MgmtNo != "A-1" || items[0].RoadAddr == "" {
		t.Fatalf("bad parse: %+v", items)
	}
}

func TestParseErrors(t *testing.T) {
	if _, _, err := ParsePage([]byte(`<OpenAPI_ServiceResponse><cmmMsgHeader><errMsg>SERVICE ERROR</errMsg><returnReasonCode>30</returnReasonCode></cmmMsgHeader></OpenAPI_ServiceResponse>`)); err == nil {
		t.Fatal("XML 오류 응답은 에러여야 함")
	}
	if _, _, err := ParsePage([]byte(`{"response":{"header":{"resultCode":"30","resultMsg":"SERVICE_KEY_IS_NOT_REGISTERED_ERROR"}}}`)); err == nil {
		t.Fatal("resultCode 30 은 에러여야 함")
	}
	items, _, err := ParsePage([]byte(`{"response":{"header":{"resultCode":"03","resultMsg":"NODATA_ERROR"}}}`))
	if err != nil || len(items) != 0 {
		t.Fatalf("데이터 없음은 빈 목록이어야 함: %v %v", items, err)
	}
}

// 2026-10-18 실제 data.go.kr 응답 (일반음식점 3건)
func TestParseRealResponse(t *testing.T) {
	body, err := os.ReadFile("testdata/general_restaurants_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	items, total, err := ParsePage(body)
	if err != nil {
		t.Fatal(err)
	}
	if total != 764 || len(items) != 3 {
		t.Fatalf("total=%d len=%d", total, len(items))
	}
	b := items[0]
	want := Business{
		MgmtNo: "6520000-101-2026-00375", RegionCode: "6520000", Name: "금등갈비",
		RoadAddr: "제주특별자치도 서귀포시 일주동로 8720, 1층 (서홍동)", LotAddr: "제주특별자치도 서귀포시 서홍동 445-10",
		PermitDate: "2026-10-16", Status: "01", StatusName: "영업/정상", BizType: "한식", Area: "71.72",
		UpdatedAt: "2026-10-17 22:47:52",
	}
	b.Raw = nil
	if !reflect.DeepEqual(b, want) {
		t.Fatalf("\n got %+v\nwant %+v", b, want)
	}
	if items[2].RoadAddr != "" || items[2].LotAddr == "" {
		t.Fatalf("도로명주소 없는 업소: %+v", items[2])
	}
}
