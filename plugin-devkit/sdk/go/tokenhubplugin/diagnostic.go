package tokenhubplugin

import (
	"fmt"
	"io"
)

func diagnosticExit(writer io.Writer, exitCode int, format string, args ...any) int {
	if _, err := fmt.Fprintf(writer, format+"\n", args...); err != nil {
		return 1
	}
	return exitCode
}
