package render

import (
	"io"

	"github.com/xuri/excelize/v2"
)

type XLSXWriter struct {
	file    *excelize.File
	sheet   string
	currRow int
}

func NewXLSXWriter() *XLSXWriter {
	f := excelize.NewFile()
	sheet := "Sheet1"
	return &XLSXWriter{
		file:    f,
		sheet:   sheet,
		currRow: 1,
	}
}

func (x *XLSXWriter) WriteRow(row []interface{}) error {
	for i, v := range row {
		cell, _ := excelize.CoordinatesToCellName(i+1, x.currRow)
		x.file.SetCellValue(x.sheet, cell, v)
	}
	x.currRow++
	return nil
}

func (x *XLSXWriter) Write(w io.Writer) error {
	return x.file.Write(w)
}

func (x *XLSXWriter) Close() error {
	return x.file.Close()
}
