package himama

import (
	"fmt"
	"strings"
	"time"
)

type Report struct {
	Date string
	URL  string
}

func (r *Report) DateISO() (string, error) {
	raw := strings.TrimSpace(r.Date)
	switch strings.ToLower(raw) {
	case "today":
		return time.Now().Format("2006-01-02"), nil
	case "yesterday":
		return time.Now().AddDate(0, 0, -1).Format("2006-01-02"), nil
	}
	t, err := time.Parse("Jan 2, 2006", stripDaySuffix(raw))
	if err != nil {
		return "", fmt.Errorf("unexpected date format %q, expected e.g. \"Sep 1, 2026 (Tue)\"", r.Date)
	}
	return t.Format("2006-01-02"), nil
}

func stripDaySuffix(s string) string {
	if idx := strings.Index(s, " ("); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return s
}
