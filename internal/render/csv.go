package render

import (
	"encoding/csv"
	"fmt"
	"io"
)

type CSVWriter struct {
	writer *csv.Writer
}

func NewCSVWriter(w io.Writer) *CSVWriter {
	return &CSVWriter{
		writer: csv.NewWriter(w),
	}
}

func (c *CSVWriter) WriteRow(row []interface{}) error {
	strRow := make([]string, len(row))
	for i, v := range row {
		strRow[i] = fmt.Sprintf("%v", v)
	}
	return c.writer.Write(strRow)
}

func (c *CSVWriter) Flush() {
	c.writer.Flush()
}

func (c *CSVWriter) Error() error {
	return c.writer.Error()
}
