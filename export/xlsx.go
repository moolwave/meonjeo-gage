// Package export 는 리드 목록을 엑셀(.xlsx) 파일로 쓴다.
// 외부 라이브러리 없이 xlsx 의 최소 구성(zip + XML)을 직접 만든다.
package export

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"
)

type Column struct {
	Title string
	Width float64
}

// WriteXLSX 는 첫 행을 굵은 머리글로, 나머지를 문자열 셀로 쓴다.
// 머리글 고정, 자동 필터, 열 너비가 설정된 시트 하나짜리 파일을 만든다.
func WriteXLSX(path, sheetName string, cols []Column, rows [][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)

	files := []struct{ name, body string }{
		{"[Content_Types].xml", contentTypes},
		{"_rels/.rels", rootRels},
		{"xl/workbook.xml", fmt.Sprintf(workbook, esc(sheetName))},
		{"xl/_rels/workbook.xml.rels", workbookRels},
		{"xl/styles.xml", styles},
		{"xl/worksheets/sheet1.xml", sheetXML(cols, rows)},
	}
	for _, file := range files {
		w, err := zw.Create(file.name)
		if err != nil {
			f.Close()
			return err
		}
		if _, err := io.WriteString(w, file.body); err != nil {
			f.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func sheetXML(cols []Column, rows [][]string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	sb.WriteString(`<sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>`)
	sb.WriteString(`<cols>`)
	for i, c := range cols {
		fmt.Fprintf(&sb, `<col min="%d" max="%d" width="%.1f" customWidth="1"/>`, i+1, i+1, c.Width)
	}
	sb.WriteString(`</cols><sheetData>`)

	writeRow := func(r int, cells []string, style int) {
		fmt.Fprintf(&sb, `<row r="%d">`, r)
		for i, v := range cells {
			fmt.Fprintf(&sb, `<c r="%s%d" t="inlineStr" s="%d"><is><t xml:space="preserve">%s</t></is></c>`,
				colName(i), r, style, esc(v))
		}
		sb.WriteString(`</row>`)
	}
	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = c.Title
	}
	writeRow(1, header, 1)
	for i, row := range rows {
		writeRow(i+2, row, 0)
	}
	sb.WriteString(`</sheetData>`)
	if len(cols) > 0 {
		fmt.Fprintf(&sb, `<autoFilter ref="A1:%s%d"/>`, colName(len(cols)-1), len(rows)+1)
	}
	sb.WriteString(`</worksheet>`)
	return sb.String()
}

// colName: 0 → A, 25 → Z, 26 → AA
func colName(i int) string {
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return s
}

func esc(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(stripControl(s)))
	return b.String()
}

// 엑셀이 거부하는 제어문자 제거 (탭·줄바꿈 제외)
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return -1
		}
		return r
	}, s)
}

const contentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>
</Types>`

const rootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`

const workbook = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<sheets><sheet name="%s" sheetId="1" r:id="rId1"/></sheets>
</workbook>`

const workbookRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>`

// 스타일 0: 기본, 스타일 1: 굵은 글씨 + 연한 회색 배경(머리글)
const styles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
<fonts count="2"><font><sz val="11"/><name val="맑은 고딕"/></font><font><b/><sz val="11"/><name val="맑은 고딕"/></font></fonts>
<fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FFE7E6E6"/><bgColor indexed="64"/></patternFill></fill></fills>
<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>
<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>
<cellXfs count="2"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0" applyFont="1" applyFill="1"/></cellXfs>
<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>
</styleSheet>`
