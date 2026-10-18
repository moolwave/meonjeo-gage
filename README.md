# 먼저가게 — 신규 개업 업소 영업 리드 수집기

> 이번 주 새로 문 연 가게, 경쟁사보다 먼저.

공공데이터포털의 **행정안전부 지방행정 인허가 API**에서 최근 인허가를 받은 업소를 매일 모아,
세부 업종 분류와 영업 포인트를 붙여 **엑셀 리드 리스트**로 만들어 줍니다.

Go 표준 라이브러리만 사용해서 설치할 패키지가 없습니다. (Go 1.24 이상)

```
수집(collect) → 저장(data/leads.json, 중복 제거) → 분류(classify, AI 또는 규칙) → 엑셀(export)
```

## 1. 준비

1. [공공데이터포털](https://www.data.go.kr) 가입
2. **행정안전부_식품_일반음식점 조회서비스** 활용신청 (자동승인)
   - 다른 업종도 같은 방식: "행정안전부_" 로 검색하면 195종이 나옵니다.
3. 마이페이지에서 **디코딩(Decoding) 인증키**를 복사
   - 인코딩 키를 넣으면 이중 인코딩되어 인증 오류(30)가 납니다.
4. (선택) Claude API 키 — 없으면 상호명 키워드 규칙으로 분류합니다.

```bash
export DATA_GO_KR_KEY='디코딩 인증키'
export ANTHROPIC_API_KEY='sk-ant-...'   # 선택
go build -o meonjeo .
```

Windows PowerShell 이면 `$env:DATA_GO_KR_KEY='...'` 로 설정하고 `go build -o meonjeo.exe .`

## 2. 처음 한 번: 응답 확인 (probe)

```bash
./meonjeo probe -services general_restaurants -days 3
```

실제 응답 JSON과 해석 결과를 보여 줍니다. 확인할 것:

- 사업장명·주소·인허가일이 제대로 읽히는지
- 안 읽히면 `localdata/parse.go` 의 `aliases` 에 응답의 필드명을 추가
- 날짜 조건이 안 먹으면 `-date-format 2006-01-02` 처럼 형식을 바꿔 보기

## 3. 실행

```bash
# 주소로 지역 지정 (가장 쉬움): 최근 7일 신규 음식점 → 엑셀
./meonjeo run -services general_restaurants -addr "서울특별시 마포구" -days 7

# 자치단체코드로 지정
./meonjeo run -services general_restaurants -region 자치단체코드 -days 7

# 단계별로 실행할 수도 있습니다
./meonjeo collect  -services general_restaurants -region 자치단체코드 -days 7
./meonjeo classify
./meonjeo export   -days 7 -out 이번주_신규개업.xlsx
```

| 옵션 | 설명 | 기본값 |
|---|---|---|
| `-services` | 업종 서비스명, 쉼표로 여러 개 | `general_restaurants` |
| `-addr` | 도로명주소에 포함된 지역명 (예: "서울특별시 마포구") | 없음 |
| `-region` | 개방자치단체코드 (비우면 전국) | 전국 |
| `-include-temp` | 축제·행사장 등 임시 업소도 엑셀에 포함 | false (제외) |
| `-days` | 최근 며칠간 인허가된 업소 | 7 |
| `-audience` | 영업 포인트를 쓸 대상 | 개업 필수 서비스 영업사원 |
| `-model` | 분류용 Claude 모델 | `claude-haiku-5-5` |
| `-no-ai` | AI 없이 규칙으로만 분류 | false |

- **자치단체코드**: API 상세 페이지의 참고문서(`개방자치단체코드_영업상태코드.xlsx`)에 있습니다.
  probe 결과의 `OPN_ATMY_GRP_CD` 값으로도 알 수 있습니다. (예: 6510000 제주시, 6520000 서귀포시)
- **`-addr` 주의**: 도로명주소가 비어 있는 업소(행사장 부스 등)는 주소 검색에 걸리지 않습니다.
- **다른 업종의 서비스명**: API 상세 페이지 "Service URL" 의 마지막 부분입니다.
  예) `https://apis.data.go.kr/1741000/general_restaurants` → `general_restaurants`

## 4. 매일 자동 실행

데이터는 매일 갱신되므로 하루 한 번 돌리면 됩니다. 이미 받은 업소는 중복 저장되지 않습니다.

```bash
# macOS/Linux crontab: 매일 아침 7시
0 7 * * * cd /경로/meonjeo-gage && ./meonjeo run -region 자치단체코드 -days 7 >> run.log 2>&1
```

Windows 는 "작업 스케줄러"에 같은 명령을 등록하면 됩니다.

## 5. 구조

```
main.go              명령(probe/collect/classify/export/run)
localdata/client.go  data.go.kr 호출, 페이지 넘기기, 호출 간격 조절
localdata/parse.go   응답 해석 (필드명이 달라도 맞춰 읽음)
store/store.go       JSON 파일 저장소 (중복 제거, 최초 수집일 보존)
classify/classify.go Claude 30건 묶음 분류 + 규칙 분류 대체
export/xlsx.go       외부 라이브러리 없는 엑셀 생성
```

테스트: `go test ./...` (가짜 API 서버로 수집→분류→엑셀 전체 흐름 확인)

## 6. 주의할 점

- **개발계정은 하루 10,000회 호출**입니다. 한 페이지 100건이라 지역 단위면 넉넉합니다.
  서비스를 운영할 땐 활용사례를 등록하고 운영계정으로 늘리세요.
- 1인 사업장은 집 주소와 같은 경우가 있어 **상세주소가 비식별 처리**되어 나올 수 있습니다.
- 공개 데이터에는 **대표자 연락처가 없습니다.** 방문 영업·우편 DM 용 리스트로 판매하고,
  전화·문자·이메일 광고는 수신자 사전 동의가 필요하다는 점을 이용약관에 명시하세요.
- 좌표가 필요하면 응답 원본(`raw`)에 들어 있고, 좌표계는 EPSG:5174 입니다.
- 축제·행사장 부스처럼 며칠만 여는 업소도 인허가 목록에 섞여 나옵니다.
  상호·주소에 '축제', '행사장' 등이 있으면 엑셀에서 기본으로 빼고 개수를 알려 줍니다.

## 7. 다음 단계 아이디어

- 고객별 설정(지역·업종·영업 대상)을 받아 각자 엑셀을 만들어 메일로 보내기
- 저장소를 SQLite 로 교체 (전국 단위로 커질 때)
- 간단한 웹 대시보드와 결제 붙이기
