package catalog

import (
	"archive/zip"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
)

type row struct {
	file   string
	line   int
	cols   map[string]int
	fields []string
}

func (r *row) get(col string) string {
	i, ok := r.cols[col]
	if !ok || i >= len(r.fields) {
		return ""
	}
	return strings.TrimSpace(r.fields[i])
}

func (r *row) errorf(format string, args ...any) error {
	return fmt.Errorf("%s line %d: %s", r.file, r.line, fmt.Sprintf(format, args...))
}

// readTable calls fn for every row of file. A missing optional file has no rows.
func readTable(zr *zip.Reader, file string, required bool, columns []string, fn func(*row) error) error {
	f, err := zr.Open(file)
	if !required && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	defer func() { _ = f.Close() }()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.ReuseRecord = true
	header, err := r.Read()
	if err != nil {
		return fmt.Errorf("%s: header: %w", file, err)
	}
	cur := &row{file: file, cols: make(map[string]int, len(header))}
	for i, name := range header {
		cur.cols[strings.TrimSpace(strings.TrimPrefix(name, "\uFEFF"))] = i
	}
	for _, col := range columns {
		if _, ok := cur.cols[col]; !ok {
			return fmt.Errorf("%s: missing column %s", file, col)
		}
	}

	for {
		fields, err := r.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		cur.fields = fields
		cur.line, _ = r.FieldPos(0)
		if err := fn(cur); err != nil {
			return err
		}
	}
}
