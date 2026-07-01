package main

import (
	"log"
	"strings"
)

type logLevel int

const (
	levelDebug logLevel = iota
	levelInfo
	levelWarning
	levelError
)

var minLogLevel = levelInfo

func setLogLevel(value string) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "DEBUG":
		minLogLevel = levelDebug
	case "WARNING", "WARN":
		minLogLevel = levelWarning
	case "ERROR":
		minLogLevel = levelError
	default:
		minLogLevel = levelInfo
	}
}

func logAt(level logLevel, label, format string, args ...interface{}) {
	if level < minLogLevel {
		return
	}
	log.Printf(label+" "+format, args...)
}

func logInfo(format string, args ...interface{})  { logAt(levelInfo, "INFO", format, args...) }
func logWarn(format string, args ...interface{})  { logAt(levelWarning, "WARNING", format, args...) }
func logError(format string, args ...interface{}) { logAt(levelError, "ERROR", format, args...) }
