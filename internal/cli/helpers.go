package cli

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// Command aliases cobra.Command for handler signatures.
type Command = cobra.Command

func currentExecutable() (string, error) {
	return os.Executable()
}

func unmarshalJSON(raw []byte, target any) error {
	return json.Unmarshal(raw, target)
}

func jsonMarshalIndent(value any) ([]byte, error) {
	return json.MarshalIndent(value, "", "  ")
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
