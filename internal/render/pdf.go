package render

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type PDFWriter struct {
	rows [][]string
}

func NewPDFWriter() *PDFWriter {
	return &PDFWriter{
		rows: make([][]string, 0),
	}
}

func (p *PDFWriter) WriteRow(row []interface{}) error {
	strRow := make([]string, len(row))
	for i, v := range row {
		strRow[i] = fmt.Sprintf("%v", v)
	}
	p.rows = append(p.rows, strRow)
	return nil
}

func (p *PDFWriter) WriteTo(w io.Writer, title string) error {
	pdf := newSimplePDF(title)
	pdf.addTable(p.rows)
	_, err := w.Write(pdf.bytes())
	return err
}

type simplePDF struct {
	title string
	pages []string
}

func newSimplePDF(title string) *simplePDF {
	return &simplePDF{
		title: title,
		pages: make([]string, 0),
	}
}

func (p *simplePDF) addTable(rows [][]string) {
	const (
		pageWidth  = 842.0
		pageHeight = 595.0
		margin     = 36.0
		rowHeight  = 16.0
	)

	if len(rows) == 0 {
		p.pages = append(p.pages, p.renderPage(nil, nil, pageWidth, pageHeight, margin, rowHeight))
		return
	}

	header := rows[0]
	body := rows[1:]
	usableHeight := pageHeight - 120
	rowsPerPage := int(usableHeight / rowHeight)
	if rowsPerPage < 1 {
		rowsPerPage = 1
	}

	for start := 0; start < len(body) || start == 0; start += rowsPerPage {
		end := start + rowsPerPage
		if end > len(body) {
			end = len(body)
		}
		p.pages = append(
			p.pages,
			p.renderPage(header, body[start:end], pageWidth, pageHeight, margin, rowHeight),
		)
		if len(body) == 0 {
			break
		}
	}
}

func (p *simplePDF) renderPage(header []string, rows [][]string, pageWidth, pageHeight, margin, rowHeight float64) string {
	var content bytes.Buffer

	writeText(&content, margin, pageHeight-margin, 14, p.title)
	y := pageHeight - margin - 28

	colCount := len(header)
	if colCount == 0 && len(rows) > 0 {
		colCount = len(rows[0])
	}
	if colCount == 0 {
		writeText(&content, margin, y, 10, "No rows found")
		return content.String()
	}

	colWidth := (pageWidth - (margin * 2)) / float64(colCount)
	writeTableRow(&content, header, margin, y, colWidth, 9)
	y -= rowHeight

	for _, row := range rows {
		writeTableRow(&content, row, margin, y, colWidth, 8)
		y -= rowHeight
	}

	return content.String()
}

func (p *simplePDF) bytes() []byte {
	var out bytes.Buffer
	objects := make([]string, 0)

	pageCount := len(p.pages)
	pagesObjectID := 2
	fontObjectID := 3
	firstPageObjectID := 4

	objects = append(objects, "<< /Type /Catalog /Pages 2 0 R >>")

	kids := make([]string, 0, pageCount)
	for i := 0; i < pageCount; i++ {
		kids = append(kids, fmt.Sprintf("%d 0 R", firstPageObjectID+(i*2)))
	}
	objects = append(objects, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), pageCount))
	objects = append(objects, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	for i, page := range p.pages {
		pageObjectID := firstPageObjectID + (i * 2)
		contentObjectID := pageObjectID + 1
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 842 595] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>", pagesObjectID, fontObjectID, contentObjectID))
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(page), page))
	}

	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, 0, len(objects)+1)
	offsets = append(offsets, 0)
	for i, obj := range objects {
		offsets = append(offsets, out.Len())
		out.WriteString(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, obj))
	}

	xrefOffset := out.Len()
	out.WriteString(fmt.Sprintf("xref\n0 %d\n", len(objects)+1))
	out.WriteString("0000000000 65535 f \n")
	for _, offset := range offsets[1:] {
		out.WriteString(fmt.Sprintf("%010d 00000 n \n", offset))
	}
	out.WriteString(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefOffset))

	return out.Bytes()
}

func writeTableRow(content *bytes.Buffer, row []string, x, y, colWidth, fontSize float64) {
	for i, value := range row {
		maxChars := int(colWidth / (fontSize * 0.55))
		if maxChars < 4 {
			maxChars = 4
		}
		writeText(content, x+(float64(i)*colWidth), y, fontSize, truncate(value, maxChars))
	}
}

func writeText(content *bytes.Buffer, x, y, fontSize float64, text string) {
	content.WriteString("BT\n")
	content.WriteString(fmt.Sprintf("/F1 %s Tf\n", formatFloat(fontSize)))
	content.WriteString(fmt.Sprintf("1 0 0 1 %s %s Tm\n", formatFloat(x), formatFloat(y)))
	content.WriteString(fmt.Sprintf("(%s) Tj\n", escapePDFText(text)))
	content.WriteString("ET\n")
}

func truncate(value string, maxChars int) string {
	if len(value) <= maxChars {
		return value
	}
	if maxChars <= 3 {
		return value[:maxChars]
	}
	return value[:maxChars-3] + "..."
}

func escapePDFText(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "(", "\\(")
	value = strings.ReplaceAll(value, ")", "\\)")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 2, 64)
}
