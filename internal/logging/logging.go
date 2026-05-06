package logging

import (
	"fmt"
	"log"
	"strings"
)

type Logger struct{}

func New() *Logger { return &Logger{} }

func (l *Logger) Info(msg string, kv ...interface{})  { log.Println("[INFO]", msg, formatKV(kv...)) }
func (l *Logger) Warn(msg string, kv ...interface{})  { log.Println("[WARN]", msg, formatKV(kv...)) }
func (l *Logger) Error(msg string, kv ...interface{}) { log.Println("[ERROR]", msg, formatKV(kv...)) }

func formatKV(kv ...interface{}) string {
	if len(kv) == 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteString(fmt.Sprint(kv[i]))
		b.WriteString("=")
		b.WriteString(fmt.Sprint(kv[i+1]))
		b.WriteString(" ")
	}
	return strings.TrimSpace(b.String())
}
