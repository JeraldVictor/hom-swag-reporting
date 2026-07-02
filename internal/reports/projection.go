package reports

import "fmt"

type ProjectionSink struct {
	inner   RowSink
	indexes []int
	wrote   bool
}

type ProjectionOptions struct {
	AllowSensitive bool
}

func NewProjectionSink(executor Executor, selectedColumns []string, inner RowSink) (RowSink, error) {
	return NewProjectionSinkWithOptions(executor, selectedColumns, inner, ProjectionOptions{
		AllowSensitive: true,
	})
}

func NewProjectionSinkWithOptions(executor Executor, selectedColumns []string, inner RowSink, options ProjectionOptions) (RowSink, error) {
	provider, ok := executor.(ColumnProvider)
	if len(selectedColumns) == 0 {
		if options.AllowSensitive {
			return inner, nil
		}
		if !ok {
			return inner, nil
		}
		indexes := make([]int, 0)
		for index, column := range provider.Columns() {
			if !column.Sensitive {
				indexes = append(indexes, index)
			}
		}
		return &ProjectionSink{inner: inner, indexes: indexes}, nil
	}

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
		if columns[index].Sensitive && !options.AllowSensitive {
			return nil, fmt.Errorf("selected column requires sensitive report permission: %s", key)
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
