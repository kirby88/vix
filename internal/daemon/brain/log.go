package brain

import "log"

func LogInfo(format string, args ...any)  { log.Printf("\033[34m"+format+"\033[0m", args...) }
func LogWarn(format string, args ...any)  { log.Printf("\033[33m"+format+"\033[0m", args...) }
func LogError(format string, args ...any) { log.Printf("\033[31m"+format+"\033[0m", args...) }
