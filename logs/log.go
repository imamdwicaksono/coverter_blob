package logs

import (
	"bufio"
	"os"
)

var logWriter *bufio.Writer

func SetLog(filePath string) *bufio.Writer {
	logFile, _ := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	logWriter = bufio.NewWriter(logFile)

	return logWriter
}

func LogFlush(logWriter *bufio.Writer) {
	if logWriter != nil {
		defer logWriter.Flush()
	}
}
