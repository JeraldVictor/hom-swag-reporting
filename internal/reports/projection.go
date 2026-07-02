package reports

import "fmt"

type ProjectionSink struct {
	inner   RowSink
	indexes []int
	wrote   bool
}

func NewProjectionSink(executor Executor, selectedColumns []string, inner RowSink) (RowSink, error) {
	if len(selectedColumns) == 0 {
		return inner, nil
	}

	provider, ok := executor.(ColumnProvider)
	if !ok {
		return nil, fmt.Errorf("report %s v%d does not support column selection", executor.Key(), executor.Version())
	}

	columns := provider.Columns()
	columnIndexByKey := make(map[string]int, len(columns))
	for index, column := range columns {
		columnIndexByKey[column.Key] = index
	}

	seen := make(map[string]struct{}, len(selectedColumns))
	indexes := make([]int, 0, len(selectedColumns))
	for _, key := range selectedColumns {
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate selected column: %s", key)
		}
		index, ok := columnIndexByKey[key]
		if !ok {
			return nil, fmt.Errorf("invalid selected column: %s", key)
		}
		seen[key] = struct{}{}
		indexes = append(indexes, index)
	}

	return &ProjectionSink{inner: inner, indexes: indexes}, nil
}

func (s *ProjectionSink) WriteRow(row []interface{}) error {
	projected := make([]interface{}, 0, len(s.indexes))
	for _, index := range s.indexes {
		if index >= len(row) {
			projected = append(projected, nil)
			continue
		}
		projected = append(projected, row[index])
	}
	s.wrote = true
	return s.inner.WriteRow(projected)
}
