package logger

import (
	"fmt"
	"log"
	"os"
)

const (
	// Prefix
	InfoPrefix  = "[INFO] "
	ErrorPrefix = "[ERROR] "
	FatalPrefix = "[FATAL] "

	// Color
	Reset  = "\033[0m"
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Blue   = "\033[34m"
	Purple = "\033[35m"
	Cyan   = "\033[36m"
	Gray   = "\033[37m"
	White  = "\033[97m"

	// background
	BgRed = "\033[41m"
)

func Infoln(v ...interface{}) {
	log.New(os.Stderr, Cyan+InfoPrefix+Reset, log.LstdFlags).Output(2, fmt.Sprintln(v...))
}

func Infof(format string, v ...interface{}) {
	log.New(os.Stderr, Cyan+InfoPrefix+Reset, log.LstdFlags).Output(2, fmt.Sprintf(format, v...))
}

func Errorln(v ...interface{}) {
	log.New(os.Stderr, Red+ErrorPrefix+Reset, log.LstdFlags|log.Llongfile).Output(2, fmt.Sprintln(v...))
}

func Errorf(format string, v ...interface{}) {
	log.New(os.Stderr, Red+ErrorPrefix+Reset, log.LstdFlags|log.Llongfile).Output(2, fmt.Sprintf(format, v...))
}

func Fatalf(format string, v ...interface{}) {
	log.New(os.Stderr, Red+FatalPrefix, log.LstdFlags|log.Llongfile).Output(2, fmt.Sprintf(format, v...)+Reset)
	os.Exit(1)
}

func Fatalln(v ...interface{}) {
	log.New(os.Stderr, BgRed+FatalPrefix, log.LstdFlags|log.Llongfile).Output(2, fmt.Sprintln(v...)+Reset)
	os.Exit(1)
}
