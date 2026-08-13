// Package output implements the unified JSON envelope contract shared by every
// anp-cli command, plus the --format / --jq / --dry-run rendering pipeline.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/itchyny/gojq"
)

type Format string

const (
	FormatJSON   Format = "json"
	FormatPretty Format = "pretty"
	FormatTable  Format = "table"
)

type IdentityMeta struct {
	Name string `json:"name,omitempty"`
	DID  string `json:"did,omitempty"`
}

type Meta struct {
	Version  string        `json:"version"`
	Identity *IdentityMeta `json:"identity,omitempty"`
	DryRun   bool          `json:"dry_run"`
	Format   string        `json:"format"`
}

// SuccessEnvelope is the standard successful command result.
type SuccessEnvelope struct {
	OK       bool           `json:"ok"`
	Command  string         `json:"command"`
	Data     any            `json:"data,omitempty"`
	Plan     any            `json:"plan,omitempty"`
	Warnings []string       `json:"warnings,omitempty"`
	Summary  string         `json:"summary,omitempty"`
	Meta     Meta           `json:"meta"`
	Notice   map[string]any `json:"_notice,omitempty"`
}

type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Hint      string `json:"hint,omitempty"`
	Retryable bool   `json:"retryable"`
	Details   any    `json:"details,omitempty"`
}

type ErrorEnvelope struct {
	OK     bool           `json:"ok"`
	Error  ErrorDetail    `json:"error"`
	Meta   Meta           `json:"meta"`
	Notice map[string]any `json:"_notice,omitempty"`
}

type ExitError struct {
	Code   int
	Detail ErrorDetail
}

func (e *ExitError) Error() string {
	if e == nil {
		return ""
	}
	return e.Detail.Message
}

func NewExitError(code string, exitCode int, message string, hint string) *ExitError {
	return &ExitError{
		Code: exitCode,
		Detail: ErrorDetail{
			Code:      code,
			Message:   message,
			Hint:      hint,
			Retryable: false,
		},
	}
}

func RetryableExitError(code string, exitCode int, message string, hint string) *ExitError {
	return &ExitError{
		Code: exitCode,
		Detail: ErrorDetail{
			Code:      code,
			Message:   message,
			Hint:      hint,
			Retryable: true,
		},
	}
}

func NormalizeFormat(raw string) (Format, error) {
	normalized := Format(strings.ToLower(strings.TrimSpace(raw)))
	switch normalized {
	case FormatJSON, FormatPretty, FormatTable:
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported format %q", raw)
	}
}

func RenderSuccess(w io.Writer, format Format, jqExpr string, envelope SuccessEnvelope) error {
	return render(w, format, jqExpr, envelope)
}

func RenderError(w io.Writer, format Format, jqExpr string, envelope ErrorEnvelope) error {
	return render(w, format, jqExpr, envelope)
}

func render(w io.Writer, format Format, jqExpr string, envelope any) error {
	value, err := toGeneric(envelope)
	if err != nil {
		return err
	}
	if strings.TrimSpace(jqExpr) != "" {
		value, err = applyJQ(value, jqExpr)
		if err != nil {
			return err
		}
	}
	return writeValue(w, format, value)
}

func toGeneric(value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func applyJQ(value any, expr string) (any, error) {
	query, err := gojq.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid jq expression: %w", err)
	}
	iter := query.Run(value)
	results := make([]any, 0)
	for {
		item, ok := iter.Next()
		if !ok {
			break
		}
		if jqErr, ok := item.(error); ok {
			return nil, fmt.Errorf("jq execution failed: %w", jqErr)
		}
		results = append(results, item)
	}
	if len(results) == 0 {
		// An empty jq result should render as an empty array, not JSON null, so
		// consumers can distinguish "no matches" from "a field that is null".
		return []any{}, nil
	}
	if len(results) == 1 {
		return results[0], nil
	}
	return results, nil
}

func writeValue(w io.Writer, format Format, value any) error {
	switch format {
	case FormatJSON, FormatPretty:
		return writeJSON(w, value, true)
	case FormatTable:
		return writeTable(w, value)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func writeJSON(w io.Writer, value any, indent bool) error {
	var (
		raw []byte
		err error
	)
	if indent {
		raw, err = json.MarshalIndent(value, "", "  ")
	} else {
		raw, err = json.Marshal(value)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(raw))
	return err
}

func writeTable(w io.Writer, value any) error {
	value = tableViewValue(value)
	if rows, ok := value.([]any); ok {
		return writeTableRows(w, rows)
	}
	if object, ok := value.(map[string]any); ok {
		return writeTableObject(w, object)
	}
	return writeJSON(w, value, true)
}

func tableViewValue(value any) any {
	value = unwrapTableEnvelope(value)
	if rows, ok := preferredTableRows(value); ok {
		return rows
	}
	return value
}

func unwrapTableEnvelope(value any) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	okValue, hasOK := object["ok"].(bool)
	if !hasOK {
		return value
	}
	if okValue {
		if data, exists := object["data"]; exists {
			return data
		}
		return value
	}
	if detail, exists := object["error"]; exists {
		return detail
	}
	return value
}

func preferredTableRows(value any) ([]any, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	for _, key := range []string{"rows", "items", "messages", "members", "identities", "groups", "checks", "commands", "agents"} {
		if rows, ok := tableRows(object[key]); ok {
			return rows, true
		}
	}
	sliceKeys := make([]string, 0, len(object))
	for key, item := range object {
		if _, ok := tableRows(item); ok {
			sliceKeys = append(sliceKeys, key)
		}
	}
	if len(sliceKeys) != 1 {
		return nil, false
	}
	return tableRows(object[sliceKeys[0]])
}

func tableRows(value any) ([]any, bool) {
	rows, ok := value.([]any)
	return rows, ok
}

func writeTableObject(w io.Writer, value map[string]any) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cell, err := tableCell(value[key])
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", key, cell); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeTableRows(w io.Writer, rows []any) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "No rows")
		return err
	}
	objects := make([]map[string]any, 0, len(rows))
	columnsSet := map[string]struct{}{}
	for _, row := range rows {
		object, ok := row.(map[string]any)
		if !ok {
			return writeJSON(w, rows, true)
		}
		objects = append(objects, object)
		for key := range object {
			columnsSet[key] = struct{}{}
		}
	}
	columns := make([]string, 0, len(columnsSet))
	for column := range columnsSet {
		columns = append(columns, column)
	}
	sort.Strings(columns)

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.Join(columns, "\t")); err != nil {
		return err
	}
	for _, row := range objects {
		cells := make([]string, 0, len(columns))
		for _, column := range columns {
			cell, err := tableCell(row[column])
			if err != nil {
				return err
			}
			cells = append(cells, cell)
		}
		if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func tableCell(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case bool, float64, float32, int, int32, int64, uint, uint32, uint64:
		return fmt.Sprint(typed), nil
	default:
		buffer := bytes.NewBuffer(nil)
		if err := writeJSON(buffer, typed, false); err != nil {
			return "", err
		}
		return strings.TrimSpace(buffer.String()), nil
	}
}
