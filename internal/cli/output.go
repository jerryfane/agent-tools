package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func boolText(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

type table struct {
	headers []string
	rows    [][]string
}

func newTable(headers ...string) *table {
	return &table{headers: headers}
}

func (t *table) Add(values ...string) {
	t.rows = append(t.rows, values)
}

func (t *table) Render(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(t.headers, "\t"))
	for _, row := range t.rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	_ = tw.Flush()
}
