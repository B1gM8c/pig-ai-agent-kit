package main

import (
	"regexp"
	"strings"
)

var (
	pigAIKeyPattern    = regexp.MustCompile(`(?i)\bpigai_[A-Za-z0-9._:-]+`)
	bearerKeyPattern   = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	secretQueryPattern = regexp.MustCompile(`(?i)(api[_-]?key|token|secret|authorization)=([^&\s]+)`)
)

func redactString(value, secret string) string {
	if secret != "" {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	value = pigAIKeyPattern.ReplaceAllString(value, "pigai_[REDACTED]")
	value = bearerKeyPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = secretQueryPattern.ReplaceAllString(value, "$1=[REDACTED]")
	return value
}
